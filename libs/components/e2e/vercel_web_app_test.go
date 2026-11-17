package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Public API -----------------------------------------------------------------

func TestVercelWebApp(t *testing.T) {
	baseBackend := RequireLiveVercel(t)
	teamID, teamSlug := detectVercelTeam(t)
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "obvweb" + runID
	h := NewLiveHarness(
		t,
		"vercel-webapp",
		"openbase-vercel-webapp-fixture",
		baseBackend,
		"VERCEL_API_TOKEN="+os.Getenv("VERCEL_TOKEN"),
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_VERCEL_TEAM_ID="+teamID,
		"OPENBASE_TEST_VERCEL_TEAM_SLUG="+teamSlug,
	)

	var vercelProjectID, previewDeployID, previewAliasHost, bypassSecret string

	t.Run("when planning and applying WebApp across stage, prod, and preview", func(t *testing.T) {
		// Arrange.
		for _, s := range []string{"shared", "stage", "prod", "preview-pr-7"} {
			h.InitStack(t, s)
		}

		// Act.
		h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
		vercelProjectID = h.Output(t, "shared", "projectId")
		stageEnvID := h.Output(t, "shared", "stageCustomEnvironmentId")
		bypassSecret = h.Output(t, "shared", "protectionBypassSecret")

		envBase := []string{
			"OPENBASE_TEST_VERCEL_PROJECT_ID=" + vercelProjectID,
			"OPENBASE_TEST_STAGE_ENV_ID=" + stageEnvID,
			"OPENBASE_TEST_REVISION=a",
		}
		planOut := h.Pulumi(t, envBase, "preview", "--stack", "preview-pr-7")
		for _, s := range []string{"stage", "prod", "preview-pr-7"} {
			h.Pulumi(t, envBase, "up", "--stack", s, "--yes", "--non-interactive", "--skip-preview")
		}
		h.Pulumi(t, []string{
			"OPENBASE_TEST_VERCEL_PROJECT_ID=" + vercelProjectID,
			"OPENBASE_TEST_STAGE_ENV_ID=" + stageEnvID,
			"OPENBASE_TEST_REVISION=b",
		}, "up", "--stack", "preview-pr-7", "--yes", "--non-interactive", "--skip-preview")

		previewURL := h.Output(t, "preview-pr-7", "url")
		previewAliasHost = h.Output(t, "preview-pr-7", "aliasHost")
		previewDeployID = h.Output(t, "preview-pr-7", "deploymentId")
		stageURL := h.Output(t, "stage", "url")
		prodURL := h.Output(t, "prod", "url")

		// Assert.
		t.Run("it binds <project>-<team>.vercel.app domains/aliases, updates deployments, and scopes env vars per target", func(t *testing.T) {
			assertContains(t, planOut, "vercel:index:Deployment")
			assertContains(t, previewAliasHost, "-pr-7-"+teamSlug+".vercel.app")
			stageResp := waitForVercelProbe(t, stageURL+"/api/probe", bypassSecret)
			prodResp := waitForVercelProbe(t, prodURL+"/api/probe", bypassSecret)
			assertEqualStr(t, "stage scopedTier", stageResp.ScopedTier, "stage")
			assertEqualStr(t, "prod scopedTier", prodResp.ScopedTier, "prod")
		})

		t.Run("it rejects unauthenticated preview HTTP requests (401/403) and returns HTTP 200 with x-vercel-protection-bypass", func(t *testing.T) {
			assertUnauthenticatedPreviewBlocked(t, previewURL+"/api/probe")
			authed := waitForVercelProbe(t, previewURL+"/api/probe", bypassSecret)
			assertEqualStr(t, "preview updated revision", authed.Revision, "b")
			assertEqualStr(t, "preview scopedTier", authed.ScopedTier, "preview-pr-7")
		})
	})

	t.Run("when destroying preview", func(t *testing.T) {
		// Arrange / Act.
		h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-7", "--yes", "--non-interactive", "--skip-preview")
		h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-7", "--yes", "--non-interactive", "--skip-preview")

		// Assert.
		t.Run("it deletes the preview Alias and Deployment (404) and succeeds on repeat destroy", func(t *testing.T) {
			assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v13/deployments/%s?teamId=%s", previewDeployID, teamID), http.StatusNotFound)
			assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v4/aliases/%s?teamId=%s", previewAliasHost, teamID), http.StatusNotFound)
		})
	})

	h.OnCleanupVerify(func(t *testing.T) {
		if vercelProjectID != "" {
			assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v9/projects/%s?teamId=%s", vercelProjectID, teamID), http.StatusNotFound)
		}
	})
}

// Private helpers ------------------------------------------------------------

type vercelProbeResponse struct {
	OK         bool   `json:"ok"`
	Env        string `json:"env"`
	Revision   string `json:"revision"`
	ScopedTier string `json:"scopedTier"`
}

func waitForVercelProbe(t *testing.T, url, bypassSecret string) vercelProbeResponse {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if bypassSecret != "" {
			req.Header.Set("x-vercel-protection-bypass", bypassSecret)
		}
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err == nil {
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			var p vercelProbeResponse
			if resp.StatusCode == http.StatusOK && json.Unmarshal(raw, &p) == nil && p.OK {
				return p
			}
			last = fmt.Sprintf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
		} else {
			last = err.Error()
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("GET %s timed out waiting for 200 OK: %s", url, last)
	return vercelProbeResponse{}
}

func assertUnauthenticatedPreviewBlocked(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	loc := resp.Header.Get("Location")
	blocked := resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden ||
		((resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusTemporaryRedirect) && strings.Contains(loc, "vercel.com"))
	if !blocked {
		t.Fatalf("expected unauthenticated preview %s blocked (401/403 or 302 to vercel.com SSO), got status=%d loc=%q", url, resp.StatusCode, loc)
	}
}
