package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

func TestVercelNeonKindeApp(t *testing.T) {
	_ = RequireLiveVercel(t)
	_ = RequireLiveNeon(t)
	baseBackend := RequireLiveKinde(t)
	_, _ = RequireLiveKindeUser(t)

	teamID, teamSlug := detectVercelTeam(t)
	neonOrgID := detectNeonOrgID(t)
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "obva" + runID
	vProjName := "openbase-e2e-" + appProject
	devAppID, devAppName := prepareKindeSandboxApp(t, "dev", vProjName, appProject, teamSlug)
	prodAppID, _ := prepareKindeSandboxApp(t, "prod", vProjName, appProject, teamSlug)

	h := NewLiveHarness(
		t,
		"vercel-neon-kinde-app",
		"openbase-vercel-neon-kinde-app",
		baseBackend,
		"VERCEL_API_TOKEN="+os.Getenv("VERCEL_TOKEN"),
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_VERCEL_TEAM_ID="+teamID,
		"OPENBASE_TEST_VERCEL_TEAM_SLUG="+teamSlug,
		"NEON_ORG_ID="+neonOrgID,
		"OPENBASE_TEST_KINDE_APP_NAME="+devAppName,
		"OPENBASE_TEST_IMPORT_DEV_APP_ID="+devAppID,
		"OPENBASE_TEST_IMPORT_PROD_APP_ID="+prodAppID,
		"OPENBASE_TEST_RETAIN_KINDE_APPS=1",
	)

	var vercelProjectID, blobStoreID, blobBaseURL, blobRWToken, neonProjectID string
	t.Cleanup(func() {
		if !h.Keep {
			purgeAllStoreBlobs(blobBaseURL, blobRWToken)
		}
	})
	h.OnCleanupVerify(func(t *testing.T) {
		assertVercelNeonKindeCleanup(t, teamID, vercelProjectID, blobStoreID, neonProjectID)
	})

	t.Run("given a neokit-style vercel-neon-kinde-app consumer module", func(t *testing.T) {
		for _, stack := range []string{"shared", "dev", "preview-pr-11"} {
			h.InitStack(t, stack)
		}

		t.Run("when previewing shared, then it plans Vercel, Neon, and Kinde shared resources without mutating cloud state", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "shared")
			h.WriteArtifact(t, "shared-preview-plan.txt", planOut)

			// Assert.
			assertContains(t, planOut, "vercel:index:Project")
			assertContains(t, planOut, "neon:index:Project")
			assertContains(t, planOut, "kinde:index:Application")
		})

		t.Run("given provisioned shared, seeded dev, and preview-pr-11 stacks wired via StackReferences", func(t *testing.T) {
			h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
			vercelProjectID = h.Output(t, "shared", "vercelProjectId")
			blobStoreID = h.Output(t, "shared", "blobStoreId")
			blobBaseURL = h.Output(t, "shared", "blobBaseUrl")
			bypassSecret := h.Output(t, "shared", "protectionBypassSecret")
			neonProjectID = h.Output(t, "shared", "neonProjectId")
			devIssuer := h.Output(t, "shared", "devIssuerUrl")
			devClientSec := h.Output(t, "shared", "devClientSecret")
			blobRWToken = fetchProjectBlobRWToken(t, teamID, vercelProjectID)
			h.RegisterSecret(blobRWToken)
			h.RecordVercelBlobStore(vercelProjectID, teamID, blobBaseURL)

			stackEnv := []string{"OPENBASE_TEST_BLOB_RW_TOKEN=" + blobRWToken}
			h.Pulumi(t, stackEnv, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			devURL := h.Output(t, "dev", "url")
			devBranchID := h.Output(t, "dev", "branchId")
			h.RecordLiveURL("dev", devURL)
			_ = waitForAppHealth(t, devURL, bypassSecret)
			postAppNote(t, devURL, bypassSecret, "seeded-in-vercel-dev")
			postAppStorage(t, devURL, bypassSecret, "seed.txt", "vercel-blob-from-dev")

			h.Pulumi(t, stackEnv, "up", "--stack", "preview-pr-11", "--yes", "--non-interactive", "--skip-preview")
			previewURL := h.Output(t, "preview-pr-11", "url")
			previewAliasHost := h.Output(t, "preview-pr-11", "aliasHost")
			previewDeployID := h.Output(t, "preview-pr-11", "deploymentId")
			previewBranchID := h.Output(t, "preview-pr-11", "branchId")
			h.RecordLiveURL("preview-pr-11", previewURL)

			t.Run("when probing / and /api/health on preview-pr-11, then it serves the HTML dashboard and verifies Neon, Blob, and Kinde wiring", func(t *testing.T) {
				// Arrange / Act.
				health := waitForAppHealth(t, previewURL, bypassSecret)
				dashboardHTML := fetchAppText(t, previewURL+"/", bypassSecret)
				h.WriteJSONArtifact(t, "preview-health.json", health)

				// Assert.
				assertContains(t, dashboardHTML, "Sign in with Kinde OIDC")
				assertContains(t, dashboardHTML, "seeded-in-vercel-dev")
				assertEqualStr(t, "runtime", health.Runtime, "vercel")
				assertEqualStr(t, "environment", health.Environment, "preview-pr-11")
				assertEqualStr(t, "auth issuer", health.Auth.Issuer, devIssuer)
				assertEqualStr(t, "auth secret sha256", health.Auth.SecretSha256, sha256Hex(devClientSec))
				assertEqualStr(t, "blob fallback prefix", health.Storage.FallbackPrefix, "dev/")
				if !health.DB.Connected || !health.DB.Pooled || health.DB.NoteCount != 1 {
					t.Fatalf("expected connected pooled Neon DB with 1 inherited note, got %+v", health.DB)
				}
			})

			t.Run("when mutating notes and blobs on preview-pr-11, then it inherits dev state and isolates preview writes from dev", func(t *testing.T) {
				// Arrange / Act.
				seedRead := getAppStorage(t, previewURL, bypassSecret, "seed.txt")
				postAppNote(t, previewURL, bypassSecret, "only-in-vercel-preview")
				postAppStorage(t, previewURL, bypassSecret, "delta.txt", "preview-only-blob")
				previewAfter := waitForAppHealth(t, previewURL, bypassSecret)
				devAfter := waitForAppHealth(t, devURL, bypassSecret)
				devDeltaStatus := getAppStorageStatus(t, devURL, bypassSecret, "delta.txt")
				h.WriteJSONArtifact(t, "isolation-report.json", map[string]any{
					"fallbackRead":   seedRead,
					"previewNotes":   previewAfter.DB.NoteCount,
					"devNotes":       devAfter.DB.NoteCount,
					"devDeltaStatus": devDeltaStatus,
				})

				// Assert.
				assertEqualStr(t, "fallback blob sourcePrefix", seedRead.SourcePrefix, "dev/")
				assertEqualStr(t, "fallback blob content", seedRead.Content, "vercel-blob-from-dev")
				if previewAfter.DB.NoteCount != 2 || devAfter.DB.NoteCount != 1 {
					t.Fatalf("expected preview notes=2 and dev notes=1, got preview=%d dev=%d", previewAfter.DB.NoteCount, devAfter.DB.NoteCount)
				}
				if devDeltaStatus != http.StatusNotFound {
					t.Fatalf("expected delta.txt 404 on dev, got %d", devDeltaStatus)
				}
			})

			t.Run("when logging in and out via Kinde OIDC in headless Chrome, then both preview and default <project>.vercel.app domains succeed end-to-end", func(t *testing.T) {
				// Arrange.
				defaultProjectURL := "https://" + vProjName + ".vercel.app"
				h.RecordLiveURL("default", defaultProjectURL)

				// Act.
				defLoginStatus, defAuthLoc := callAppNoRedirect(t, defaultProjectURL+"/api/auth/login", bypassSecret)
				defKindeStatus, defKindeLoc := followAuthorizeRedirect(t, defAuthLoc)
				browserReport := verifyKindeBrowserLogin(t, h, previewURL, bypassSecret)
				h.WriteJSONArtifact(t, "auth-login-flow.json", map[string]any{
					"defaultDomainStatus": defLoginStatus,
					"defaultKindeStatus":  defKindeStatus,
					"browserLogin":        browserReport,
				})

				// Assert.
				assertAppLoginAndBrowserFlow(t, defaultProjectURL, devIssuer, defLoginStatus, defAuthLoc, defKindeStatus, defKindeLoc, browserReport)
			})

			t.Run("when destroying preview-pr-11 twice, then the preview Deployment, Alias, and Neon branch return 404 while dev stays healthy", func(t *testing.T) {
				h.SkipIfKeep(t)
				// Arrange / Act.
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-11", "--yes", "--non-interactive", "--skip-preview")
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-11", "--yes", "--non-interactive", "--skip-preview")

				// Assert.
				assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v13/deployments/%s?teamId=%s", previewDeployID, teamID), http.StatusNotFound)
				assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v4/aliases/%s?teamId=%s", previewAliasHost, teamID), http.StatusNotFound)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, previewBranchID), http.StatusNotFound)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, devBranchID), http.StatusOK)
			})
		})
	})
}

// Private helpers ------------------------------------------------------------

type fullStackHealthResponse struct {
	OK          bool   `json:"ok"`
	Runtime     string `json:"runtime"`
	Environment string `json:"environment"`
	Auth        struct {
		Issuer        string `json:"issuer"`
		ClientID      string `json:"clientId"`
		RedirectURL   string `json:"redirectUrl"`
		SecretSha256  string `json:"secretSha256"`
		Authenticated bool   `json:"authenticated"`
		User          struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"user"`
	} `json:"auth"`
	DB struct {
		Connected bool `json:"connected"`
		Pooled    bool `json:"pooled"`
		NoteCount int  `json:"noteCount"`
	} `json:"db"`
	Storage struct {
		Kind           string `json:"kind"`
		Prefix         string `json:"prefix"`
		FallbackPrefix string `json:"fallbackPrefix"`
		Bucket         string `json:"bucket"`
		FallbackBucket string `json:"fallbackBucket"`
	} `json:"storage"`
}

type appStorageReadResponse struct {
	OK           bool   `json:"ok"`
	SourcePrefix string `json:"sourcePrefix"`
	SourceBucket string `json:"sourceBucket"`
	Key          string `json:"key"`
	Content      string `json:"content"`
}

func assertAppLoginAndBrowserFlow(
	t *testing.T,
	baseURL, devIssuer string,
	loginStatus int,
	authLoc string,
	kindeStatus int,
	kindeLoc string,
	report browserAuthReport,
) {
	t.Helper()
	if loginStatus != http.StatusFound {
		t.Fatalf("expected 302 from %s/api/auth/login, got %d", baseURL, loginStatus)
	}
	assertContains(t, authLoc, devIssuer+"/oauth2/auth")
	assertLoginRedirect(t, authLoc, kindeStatus, kindeLoc)
	if !report.Authenticated || !report.LoggedOutAgain {
		t.Fatalf("expected full browser login + logout to succeed, got %+v", report)
	}
}

func assertVercelNeonKindeCleanup(t *testing.T, teamID, vercelProjectID, blobStoreID, neonProjectID string) {
	t.Helper()
	assertVercelBlobCleanup(t, teamID, vercelProjectID, blobStoreID)
	if neonProjectID != "" {
		assertNeonStatus(t, "https://console.neon.tech/api/v2/projects/"+neonProjectID, http.StatusNotFound)
	}
	if countOpenBaseKindeApps(t, "dev") > 1 || countOpenBaseKindeApps(t, "prod") > 1 {
		t.Fatal("expected at most 1 retained openbase-e2e Kinde sandbox app per tenant")
	}
}

func purgeAllStoreBlobs(baseURL, rwToken string) {
	if baseURL == "" || rwToken == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := vercel.NewBlobOverlayClient(baseURL, rwToken, "", "")
	if err != nil {
		return
	}
	_ = c.Delete(ctx, "dev/seed.txt", "preview-pr-11/delta.txt")
}

func waitForAppHealth(t *testing.T, baseURL, bypassSecret string) fullStackHealthResponse {
	t.Helper()
	deadline := time.Now().Add(75 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		raw, status, err := doAppRequest(http.MethodGet, baseURL+"/api/health", bypassSecret, "", nil)
		if err == nil && status == http.StatusOK {
			var h fullStackHealthResponse
			if json.Unmarshal(raw, &h) == nil && h.OK {
				return h
			}
		}
		last = fmt.Sprintf("status=%d err=%v body=%s", status, err, strings.TrimSpace(string(raw)))
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("GET %s/api/health timed out: %s", baseURL, last)
	return fullStackHealthResponse{}
}

func fetchAppText(t *testing.T, targetURL, bypassSecret string) string {
	t.Helper()
	raw, status, err := doAppRequest(http.MethodGet, targetURL, bypassSecret, "", nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s failed: status=%d err=%v body=%s", targetURL, status, err, string(raw))
	}
	return string(raw)
}

func postAppNote(t *testing.T, baseURL, bypassSecret, noteBody string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"body": noteBody})
	raw, status, err := doAppRequest(http.MethodPost, baseURL+"/api/notes", bypassSecret, "application/json", payload)
	if err != nil || status != http.StatusOK {
		t.Fatalf("POST %s/api/notes failed: status=%d err=%v body=%s", baseURL, status, err, string(raw))
	}
}

func postAppStorage(t *testing.T, baseURL, bypassSecret, key, content string) {
	t.Helper()
	target := fmt.Sprintf("%s/api/storage?key=%s", baseURL, url.QueryEscape(key))
	raw, status, err := doAppRequest(http.MethodPost, target, bypassSecret, "text/plain", []byte(content))
	if err != nil || status != http.StatusOK {
		t.Fatalf("POST %s failed: status=%d err=%v body=%s", target, status, err, string(raw))
	}
}

func getAppStorage(t *testing.T, baseURL, bypassSecret, key string) appStorageReadResponse {
	t.Helper()
	target := fmt.Sprintf("%s/api/storage?key=%s", baseURL, url.QueryEscape(key))
	raw, status, err := doAppRequest(http.MethodGet, target, bypassSecret, "", nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s failed: status=%d err=%v body=%s", target, status, err, string(raw))
	}
	var out appStorageReadResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal storage response: %v (%s)", err, string(raw))
	}
	return out
}

func getAppStorageStatus(t *testing.T, baseURL, bypassSecret, key string) int {
	t.Helper()
	target := fmt.Sprintf("%s/api/storage?key=%s", baseURL, url.QueryEscape(key))
	_, status, err := doAppRequest(http.MethodGet, target, bypassSecret, "", nil)
	if err != nil {
		t.Fatalf("GET %s failed: %v", target, err)
	}
	return status
}

func doAppRequest(method, targetURL, bypassSecret, contentType string, body []byte) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, targetURL, reader)
	if err != nil {
		return nil, 0, err
	}
	if bypassSecret != "" {
		req.Header.Set("x-vercel-protection-bypass", bypassSecret)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, err
}

func callAppNoRedirect(t *testing.T, targetURL, bypassSecret string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bypassSecret != "" {
		req.Header.Set("x-vercel-protection-bypass", bypassSecret)
	}
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, resp.Header.Get("Location")
}

func followAuthorizeRedirect(t *testing.T, authorizeURL string) (int, string) {
	t.Helper()
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(authorizeURL)
	if err != nil {
		t.Fatalf("GET %s: %v", authorizeURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, resp.Header.Get("Location")
}
