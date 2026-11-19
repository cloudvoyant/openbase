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

	var vercelProjectID string
	h.OnCleanupVerify(func(t *testing.T) {
		if vercelProjectID != "" {
			assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v9/projects/%s?teamId=%s", vercelProjectID, teamID), http.StatusNotFound)
		}
	})

	t.Run("given a vercel.Project and vercel.WebApp consumer program", func(t *testing.T) {
		for _, s := range []string{"shared", "stage", "prod", "preview-pr-7"} {
			h.InitStack(t, s)
		}
		h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
		vercelProjectID = h.Output(t, "shared", "projectId")
		stageEnvID := h.Output(t, "shared", "stageCustomEnvironmentId")
		bypassSecret := h.Output(t, "shared", "protectionBypassSecret")
		envRevA := []string{
			"OPENBASE_TEST_VERCEL_PROJECT_ID=" + vercelProjectID,
			"OPENBASE_TEST_STAGE_ENV_ID=" + stageEnvID,
			"OPENBASE_TEST_REVISION=a",
		}

		t.Run("when previewing preview-pr-7, then it plans vercel:index:Deployment without mutating Vercel", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, envRevA, "preview", "--stack", "preview-pr-7")
			h.WriteArtifact(t, "preview-plan.txt", planOut)

			// Assert.
			assertContains(t, planOut, "vercel:index:Deployment")
		})

		t.Run("given provisioned shared, stage, prod, and updated preview-pr-7 stacks", func(t *testing.T) {
			for _, s := range []string{"stage", "prod", "preview-pr-7"} {
				h.Pulumi(t, envRevA, "up", "--stack", s, "--yes", "--non-interactive", "--skip-preview")
			}
			envRevB := []string{
				"OPENBASE_TEST_VERCEL_PROJECT_ID=" + vercelProjectID,
				"OPENBASE_TEST_STAGE_ENV_ID=" + stageEnvID,
				"OPENBASE_TEST_REVISION=b",
			}
			h.Pulumi(t, envRevB, "up", "--stack", "preview-pr-7", "--yes", "--non-interactive", "--skip-preview")

			previewURL := h.Output(t, "preview-pr-7", "url")
			previewAliasHost := h.Output(t, "preview-pr-7", "aliasHost")
			previewDeployID := h.Output(t, "preview-pr-7", "deploymentId")
			stageURL := h.Output(t, "stage", "url")
			prodURL := h.Output(t, "prod", "url")
			h.RecordLiveURL("stage", stageURL)
			h.RecordLiveURL("prod", prodURL)
			h.RecordLiveURL("preview-pr-7", previewURL)

			t.Run("when probing stage and prod aliases, then squat-proof <project>-<team>.vercel.app hosts serve target-scoped env vars", func(t *testing.T) {
				// Arrange / Act.
				stageResp := waitForVercelProbe(t, stageURL+"/api/probe", bypassSecret)
				prodResp := waitForVercelProbe(t, prodURL+"/api/probe", bypassSecret)
				h.WriteJSONArtifact(t, "stage-prod-probes.json", map[string]any{
					"stage": stageResp,
					"prod":  prodResp,
				})

				// Assert.
				assertContains(t, previewAliasHost, "-pr-7-"+teamSlug+".vercel.app")
				assertEqualStr(t, "stage scopedTier", stageResp.ScopedTier, "stage")
				assertEqualStr(t, "prod scopedTier", prodResp.ScopedTier, "prod")
			})

			t.Run("when probing preview-pr-7 without and with x-vercel-protection-bypass", func(t *testing.T) {
				// Arrange / Act.
				unauthStatus, unauthLoc := probeUnauthenticatedPreview(t, previewURL+"/api/probe")
				authed := waitForVercelProbe(t, previewURL+"/api/probe", bypassSecret)
				h.WriteJSONArtifact(t, "preview-bypass-probe.json", map[string]any{
					"unauthenticatedStatus": unauthStatus,
					"unauthenticatedLoc":    unauthLoc,
					"authenticated":         authed,
				})

				// Assert.
				t.Run("then unauthenticated requests are blocked (401/403/302 SSO)", func(t *testing.T) {
					assertPreviewStatusBlocked(t, previewURL+"/api/probe", unauthStatus, unauthLoc)
				})

				t.Run("then bypass-authenticated requests serve HTTP 200 with the in-place updated revision", func(t *testing.T) {
					assertEqualStr(t, "preview updated revision", authed.Revision, "b")
					assertEqualStr(t, "preview scopedTier", authed.ScopedTier, "preview-pr-7")
				})
			})

			t.Run("when destroying preview-pr-7 twice, then the preview Deployment and Alias return 404", func(t *testing.T) {
				h.SkipIfKeep(t)
				// Arrange / Act.
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-7", "--yes", "--non-interactive", "--skip-preview")
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-7", "--yes", "--non-interactive", "--skip-preview")

				// Assert.
				assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v13/deployments/%s?teamId=%s", previewDeployID, teamID), http.StatusNotFound)
				assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v4/aliases/%s?teamId=%s", previewAliasHost, teamID), http.StatusNotFound)
			})
		})
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

func probeUnauthenticatedPreview(t *testing.T, url string) (int, string) {
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
	return resp.StatusCode, resp.Header.Get("Location")
}

func assertPreviewStatusBlocked(t *testing.T, url string, status int, loc string) {
	t.Helper()
	blocked := status == http.StatusUnauthorized ||
		status == http.StatusForbidden ||
		((status == http.StatusFound || status == http.StatusTemporaryRedirect) && strings.Contains(loc, "vercel.com"))
	if !blocked {
		t.Fatalf("expected unauthenticated preview %s blocked (401/403 or 302 to vercel.com SSO), got status=%d loc=%q", url, status, loc)
	}
}
