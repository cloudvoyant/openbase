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
	nonProdAppID, nonProdAppName := prepareKindeSandboxApp(t, "non-prod", vProjName, appProject, teamSlug)
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
		"OPENBASE_TEST_KINDE_APP_NAME="+nonProdAppName,
		"OPENBASE_TEST_IMPORT_NONPROD_APP_ID="+nonProdAppID,
		"OPENBASE_TEST_IMPORT_PROD_APP_ID="+prodAppID,
		"OPENBASE_TEST_RETAIN_KINDE_APPS=1",
	)

	var vercelProjectID, blobStoreID, neonProjectID string
	h.OnCleanupVerify(func(t *testing.T) {
		assertVercelNeonKindeCleanup(t, teamID, vercelProjectID, blobStoreID, neonProjectID)
	})

	t.Run("given separate infra/platform, infra/shared, and infra/env Vercel Pulumi programs", func(t *testing.T) {
		for _, stack := range []string{"platform", "shared", "dev", "prod", "stage", "preview-pr-11"} {
			h.InitStack(t, stack)
		}
		h.Pulumi(t, nil, "up", "--stack", "platform", "--yes", "--non-interactive", "--skip-preview")

		t.Run("when previewing shared, then it plans Vercel, Neon, and Kinde shared resources against platform without mutating project state", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "shared")
			h.WriteArtifact(t, "shared-preview-plan.txt", planOut)

			// Assert.
			assertContains(t, planOut, "vercel:index:Project")
			assertContains(t, planOut, "neon:index:Project")
			assertContains(t, planOut, "kinde:index:Application")
		})

		t.Run("given provisioned platform, shared, dev, prod, stage<-prod, and preview-pr-11<-dev stacks", func(t *testing.T) {
			s := deployAllVercelAppStacks(t, h, teamID, vProjName)
			vercelProjectID = s.vercelProjectID
			blobStoreID = s.blobStoreID
			neonProjectID = s.neonProjectID

			t.Run("when probing prod and stage, then stage propagates CustomEnvironment Blob vars and inherits prod Neon state with Prod Kinde auth", func(t *testing.T) {
				// Arrange / Act.
				hasStageTok, hasStageBase := inspectStageBlobEnvVars(t, teamID, s.vercelProjectID, s.stageEnvID)
				prodHealth := waitForAppHealth(t, s.prodURL, s.bypassSecret)
				stageHealth := waitForAppHealth(t, s.stageURL, s.bypassSecret)
				stageHTML := fetchAppText(t, s.stageURL+"/", s.bypassSecret)
				h.WriteJSONArtifact(t, "prod-stage-health.json", map[string]any{"prod": prodHealth, "stage": stageHealth})

				// Assert.
				if !hasStageTok || !hasStageBase {
					t.Fatalf("expected stage CustomEnvironment BLOB_READ_WRITE_TOKEN=%v BLOB_BASE_URL=%v", hasStageTok, hasStageBase)
				}
				assertProdAndStageAppHealth(t, prodHealth, stageHealth, stageHTML, "vercel", s.prodIssuer, sha256Hex(s.prodClientSec), "seeded-in-vercel-prod")
			})

			t.Run("when probing preview-pr-11 without and with x-vercel-protection-bypass, then unauthenticated requests are blocked while bypass serves the app", func(t *testing.T) {
				// Arrange / Act.
				unauthStatus, unauthLoc := probeUnauthenticatedPreview(t, s.previewURL+"/api/health")
				health := waitForAppHealth(t, s.previewURL, s.bypassSecret)
				dashboardHTML := fetchAppText(t, s.previewURL+"/", s.bypassSecret)
				h.WriteJSONArtifact(t, "preview-health.json", health)

				// Assert.
				assertPreviewStatusBlocked(t, s.previewURL+"/api/health", unauthStatus, unauthLoc)
				assertPreviewVercelAppHealth(t, health, dashboardHTML, s.devIssuer, sha256Hex(s.devClientSec))
			})

			t.Run("when mutating notes and blobs on preview-pr-11 and BlobOverlayClient, then it inherits dev state and isolates preview writes", func(t *testing.T) {
				// Arrange / Act.
				seedRead := getAppStorage(t, s.previewURL, s.bypassSecret, "seed.txt")
				postAppNote(t, s.previewURL, s.bypassSecret, "only-in-vercel-preview")
				postAppStorage(t, s.previewURL, s.bypassSecret, "delta.txt", "preview-only-blob")
				previewAfter := waitForAppHealth(t, s.previewURL, s.bypassSecret)
				devAfter := waitForAppHealth(t, s.devURL, s.bypassSecret)
				devDeltaStatus := getAppStorageStatus(t, s.devURL, s.bypassSecret, "delta.txt")
				clientSrc, clientSeed, clientIsolated := exerciseBlobOverlayClient(t, s.blobBaseURL, s.blobRWToken, "dev/", "preview-pr-11/", "dev/")
				h.WriteJSONArtifact(t, "isolation-report.json", map[string]any{
					"fallbackRead":   seedRead,
					"previewNotes":   previewAfter.DB.NoteCount,
					"devNotes":       devAfter.DB.NoteCount,
					"devDeltaStatus": devDeltaStatus,
					"clientIsolated": clientIsolated,
				})

				// Assert.
				assertAppStorageAndDBIsolation(t, seedRead.SourcePrefix, "dev/", seedRead.Content, "vercel-blob-from-dev", previewAfter.DB.NoteCount, devAfter.DB.NoteCount, devDeltaStatus)
				assertEqualStr(t, "BlobOverlayClient fallback prefix", clientSrc, "dev/")
				assertEqualStr(t, "BlobOverlayClient seed bytes", clientSeed, "vercel-blob-from-dev")
				if !clientIsolated {
					t.Fatal("expected BlobOverlayClient preview write to stay absent from dev/")
				}
			})

			t.Run("when calling /oauth2/auth with unregistered redirects vs logging in via headless Chrome, then invalid redirects fail while preview and <project>.vercel.app authenticate", func(t *testing.T) {
				// Arrange / Act.
				rejCode, rejLoc, rejBody := callKindeAuthorize(t, s.devIssuer, s.devClientID, "https://unregistered.example.com/callback")
				defLoginStatus, defAuthLoc := callAppNoRedirect(t, s.defaultProjectURL+"/api/auth/login", s.bypassSecret)
				defKindeStatus, defKindeLoc := followAuthorizeRedirect(t, defAuthLoc)
				browserReport := verifyKindeBrowserLogin(t, h, s.previewURL, s.bypassSecret)
				h.WriteJSONArtifact(t, "auth-login-flow.json", map[string]any{
					"defaultDomainStatus": defLoginStatus,
					"defaultKindeStatus":  defKindeStatus,
					"browserLogin":        browserReport,
				})

				// Assert.
				assertRedirectRejected(t, "https://unregistered.example.com/callback", rejCode, rejLoc, rejBody)
				assertAppLoginAndBrowserFlow(t, s.defaultProjectURL, s.prodIssuer, defLoginStatus, defAuthLoc, defKindeStatus, defKindeLoc, browserReport)
			})

			t.Run("when destroying preview-pr-11 twice, then the preview Deployment, Alias, and Neon branch return 404 while dev, stage, and prod stay healthy", func(t *testing.T) {
				h.SkipIfKeep(t)
				// Arrange / Act.
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-11", "--yes", "--non-interactive", "--skip-preview")
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-11", "--yes", "--non-interactive", "--skip-preview")

				// Assert.
				assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v13/deployments/%s?teamId=%s", s.previewDeployID, teamID), http.StatusNotFound)
				assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v4/aliases/%s?teamId=%s", s.previewAliasHost, teamID), http.StatusNotFound)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, s.previewBranchID), http.StatusNotFound)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, s.devBranchID), http.StatusOK)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, s.stageBranchID), http.StatusOK)
			})
		})
	})
}

// Private helpers ------------------------------------------------------------

type vercelDeployedAppState struct {
	vercelProjectID   string
	stageEnvID        string
	blobStoreID       string
	blobBaseURL       string
	blobRWToken       string
	bypassSecret      string
	neonProjectID     string
	devIssuer         string
	devClientID       string
	devClientSec      string
	prodIssuer        string
	prodClientSec     string
	defaultProjectURL string
	devURL            string
	prodURL           string
	stageURL          string
	previewURL        string
	previewAliasHost  string
	previewDeployID   string
	devBranchID       string
	stageBranchID     string
	previewBranchID   string
}

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

func deployAllVercelAppStacks(t *testing.T, h *LiveStackHarness, teamID, vProjName string) vercelDeployedAppState {
	t.Helper()
	h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
	s := vercelDeployedAppState{
		vercelProjectID:   h.Output(t, "shared", "vercelProjectId"),
		stageEnvID:        h.Output(t, "shared", "stageCustomEnvironmentId"),
		blobStoreID:       h.Output(t, "shared", "blobStoreId"),
		blobBaseURL:       h.Output(t, "shared", "blobBaseUrl"),
		bypassSecret:      h.Output(t, "shared", "protectionBypassSecret"),
		neonProjectID:     h.Output(t, "shared", "neonProjectId"),
		devIssuer:         h.Output(t, "shared", "nonProdIssuerUrl"),
		devClientID:       h.Output(t, "shared", "nonProdClientId"),
		devClientSec:      h.Output(t, "shared", "nonProdClientSecret"),
		prodIssuer:        h.Output(t, "shared", "prodIssuerUrl"),
		prodClientSec:     h.Output(t, "shared", "prodClientSecret"),
		defaultProjectURL: "https://" + vProjName + ".vercel.app",
	}
	s.blobRWToken = fetchProjectBlobRWToken(t, teamID, s.vercelProjectID)
	h.RegisterSecret(s.blobRWToken)
	h.RecordVercelBlobStore(s.vercelProjectID, teamID, s.blobBaseURL)
	baseURL, rwTok := s.blobBaseURL, s.blobRWToken
	t.Cleanup(func() {
		if !h.Keep {
			purgeAllStoreBlobs(baseURL, rwTok)
		}
	})

	stackEnv := []string{"OPENBASE_TEST_BLOB_RW_TOKEN=" + s.blobRWToken}
	h.Pulumi(t, stackEnv, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
	s.devURL = h.Output(t, "dev", "url")
	s.devBranchID = h.Output(t, "dev", "branchId")
	_ = waitForAppHealth(t, s.devURL, s.bypassSecret)
	postAppNote(t, s.devURL, s.bypassSecret, "seeded-in-vercel-dev")
	postAppStorage(t, s.devURL, s.bypassSecret, "seed.txt", "vercel-blob-from-dev")

	h.Pulumi(t, stackEnv, "up", "--stack", "prod", "--yes", "--non-interactive", "--skip-preview")
	s.prodURL = h.Output(t, "prod", "url")
	_ = waitForAppHealth(t, s.prodURL, s.bypassSecret)
	postAppNote(t, s.prodURL, s.bypassSecret, "seeded-in-vercel-prod")

	h.Pulumi(t, stackEnv, "up", "--stack", "stage", "--yes", "--non-interactive", "--skip-preview")
	s.stageURL = h.Output(t, "stage", "url")
	s.stageBranchID = h.Output(t, "stage", "branchId")

	h.Pulumi(t, stackEnv, "up", "--stack", "preview-pr-11", "--yes", "--non-interactive", "--skip-preview")
	s.previewURL = h.Output(t, "preview-pr-11", "url")
	s.previewAliasHost = h.Output(t, "preview-pr-11", "aliasHost")
	s.previewDeployID = h.Output(t, "preview-pr-11", "deploymentId")
	s.previewBranchID = h.Output(t, "preview-pr-11", "branchId")

	h.RecordLiveURL("default", s.defaultProjectURL)
	h.RecordLiveURL("dev", s.devURL)
	h.RecordLiveURL("prod", s.prodURL)
	h.RecordLiveURL("stage", s.stageURL)
	h.RecordLiveURL("preview-pr-11", s.previewURL)
	return s
}

func assertProdAndStageAppHealth(
	t *testing.T,
	prodH, stageH fullStackHealthResponse,
	stageHTML, wantRuntime, prodIssuer, prodSecHash, wantSeededNote string,
) {
	t.Helper()
	assertEqualStr(t, "prod runtime", prodH.Runtime, wantRuntime)
	assertEqualStr(t, "prod environment", prodH.Environment, "prod")
	assertEqualStr(t, "prod auth issuer", prodH.Auth.Issuer, prodIssuer)
	assertEqualStr(t, "prod auth secret sha256", prodH.Auth.SecretSha256, prodSecHash)
	assertEqualStr(t, "stage environment", stageH.Environment, "stage")
	assertEqualStr(t, "stage auth issuer", stageH.Auth.Issuer, prodIssuer)
	assertEqualStr(t, "stage auth secret sha256", stageH.Auth.SecretSha256, prodSecHash)
	assertContains(t, stageHTML, wantSeededNote)
	if prodH.DB.NoteCount != 1 || stageH.DB.NoteCount != 1 {
		t.Fatalf("expected prod notes=1 and stage<-prod inherited notes=1, got prod=%d stage=%d", prodH.DB.NoteCount, stageH.DB.NoteCount)
	}
}

func assertPreviewVercelAppHealth(t *testing.T, health fullStackHealthResponse, dashboardHTML, devIssuer, devSecHash string) {
	t.Helper()
	assertContains(t, dashboardHTML, "Sign in with Kinde OIDC")
	assertContains(t, dashboardHTML, "seeded-in-vercel-dev")
	assertEqualStr(t, "runtime", health.Runtime, "vercel")
	assertEqualStr(t, "environment", health.Environment, "preview-pr-11")
	assertEqualStr(t, "auth issuer", health.Auth.Issuer, devIssuer)
	assertEqualStr(t, "auth secret sha256", health.Auth.SecretSha256, devSecHash)
	assertEqualStr(t, "blob fallback prefix", health.Storage.FallbackPrefix, "dev/")
	if !health.DB.Connected || !health.DB.Pooled || health.DB.NoteCount != 1 {
		t.Fatalf("expected connected pooled Neon DB with 1 inherited note, got %+v", health.DB)
	}
}

func assertAppStorageAndDBIsolation(
	t *testing.T,
	gotSource, wantSource, gotContent, wantContent string,
	previewNotes, devNotes, devDeltaStatus int,
) {
	t.Helper()
	assertEqualStr(t, "fallback source", gotSource, wantSource)
	assertEqualStr(t, "fallback content", gotContent, wantContent)
	if previewNotes != 2 || devNotes != 1 {
		t.Fatalf("expected preview notes=2 and dev notes=1, got preview=%d dev=%d", previewNotes, devNotes)
	}
	if devDeltaStatus != http.StatusNotFound {
		t.Fatalf("expected delta.txt 404 on dev, got %d", devDeltaStatus)
	}
}

func assertAppLoginAndBrowserFlow(
	t *testing.T,
	baseURL, expectedIssuer string,
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
	assertContains(t, authLoc, expectedIssuer+"/oauth2/auth")
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
	if countOpenBaseKindeApps(t, "non-prod") > 1 || countOpenBaseKindeApps(t, "prod") > 1 {
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
	_ = c.Delete(ctx, "dev/seed.txt", "prod/seed.txt", "stage/seed.txt", "preview-pr-11/delta.txt", "preview-pr-11/client-delta.txt")
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
