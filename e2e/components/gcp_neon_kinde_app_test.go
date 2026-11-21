package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Public API -----------------------------------------------------------------

func TestGCPNeonKindeApp(t *testing.T) {
	gcpProject, baseBackend := RequireLiveGCP(t)
	_ = RequireLiveNeon(t)
	_ = RequireLiveKinde(t)
	_, _ = RequireLiveKindeUser(t)

	projectNumber := ResolveGCPProjectNumber(t, gcpProject)
	neonOrgID := detectNeonOrgID(t)
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "obga" + runID
	nonProdR, nonProdL := expectedGCPKindeURLs("non-prod", appProject, projectNumber)
	prodR, prodL := expectedGCPKindeURLs("prod", appProject, projectNumber)
	nonProdAppID, nonProdAppName := prepareKindeSandboxByPrefix(t, "non-prod", "openbase-e2e-gcp-", "openbase-e2e-gcp-"+appProject, nonProdR, nonProdL)
	prodAppID, _ := prepareKindeSandboxByPrefix(t, "prod", "openbase-e2e-gcp-", "openbase-e2e-gcp-"+appProject, prodR, prodL)

	h := NewLiveHarness(
		t,
		"gcp-neon-kinde-app",
		"openbase-gcp-neon-kinde-app",
		baseBackend,
		"OPENBASE_TEST_GCP_PROJECT="+gcpProject,
		"GOOGLE_PROJECT="+gcpProject,
		"OPENBASE_TEST_GCP_PROJECT_NUMBER="+projectNumber,
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_RUN_ID="+runID,
		"NEON_ORG_ID="+neonOrgID,
		"OPENBASE_TEST_KINDE_APP_NAME="+nonProdAppName,
		"OPENBASE_TEST_IMPORT_NONPROD_APP_ID="+nonProdAppID,
		"OPENBASE_TEST_IMPORT_PROD_APP_ID="+prodAppID,
		"OPENBASE_TEST_RETAIN_KINDE_APPS=1",
	)

	var state gcpDeployedAppState
	h.OnCleanupVerify(func(t *testing.T) {
		assertGCPNeonKindeCleanup(
			t,
			gcpProject,
			state.neonProjectID,
			[]string{state.devSvc, state.prodSvc, state.stageSvc, state.previewSvc},
			[]string{state.devBucket, state.prodBucket, state.stageBucket, state.previewBucket},
		)
	})

	t.Run("given separate openbase.Platform (infra/platform), openbase.Project (infra/shared), and openbase.Environment (infra/env) GCP programs", func(t *testing.T) {
		for _, stack := range []string{"platform", "shared", "dev", "prod", "stage", "preview-pr-12"} {
			h.InitStack(t, stack)
		}
		h.Pulumi(t, nil, "up", "--stack", "platform", "--yes", "--non-interactive", "--skip-preview")

		t.Run("when previewing shared, then it plans GCP, Neon, and Kinde shared resources against platform without mutating project state", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "shared")
			h.WriteArtifact(t, "shared-preview-plan.txt", planOut)

			// Assert.
			assertContains(t, planOut, "gcp:artifactregistry:Repository")
			assertContains(t, planOut, "neon:index:Project")
			assertContains(t, planOut, "kinde:index:Application")
		})

		t.Run("given provisioned platform, shared, dev, prod, stage<-prod, and preview-pr-12<-dev stacks", func(t *testing.T) {
			state = deployAllGCPAppStacks(t, h)

			t.Run("when probing prod and stage over HTTP and pgx, then stage inherits prod Neon state with Prod Kinde auth", func(t *testing.T) {
				// Arrange / Act.
				prodHealth := waitForAppHealth(t, state.prodURL, "")
				stageHealth := waitForAppHealth(t, state.stageURL, "")
				stageHTML := fetchAppText(t, state.stageURL+"/", "")
				pgxNote := querySingleText(t, state.stageDBURL, "SELECT body FROM app_notes LIMIT 1")
				h.WriteJSONArtifact(t, "prod-stage-health.json", map[string]any{"prod": prodHealth, "stage": stageHealth})

				// Assert.
				assertProdAndStageAppHealth(t, prodHealth, stageHealth, stageHTML, "gcp-cloud-run", state.prodIssuer, sha256Hex(state.prodClientSec), "seeded-in-gcp-prod")
				assertEqualStr(t, "stage<-prod pgx row", pgxNote, "seeded-in-gcp-prod")
			})

			t.Run("when destroying protected prod or stage, updating dev in place, or failing an apply, then protected resources and state stay intact", func(t *testing.T) {
				verifyGCPProtectionUpdateAndFailure(t, h, state)
			})

			t.Run("when probing /api/health and /api/sign on preview-pr-12, then it serves the dashboard, Secret Manager auth, and keyless GCS v4 signed URLs", func(t *testing.T) {
				// Arrange / Act.
				health := waitForAppHealth(t, state.previewURL, "")
				dashboardHTML := fetchAppText(t, state.previewURL+"/", "")
				signedCode, signedBody := probeSignedEndpoint(t, state.previewURL, state.previewBucket)
				h.WriteJSONArtifact(t, "preview-health.json", health)

				// Assert.
				assertPreviewGCPAppHealth(t, health, dashboardHTML, state.devIssuer, sha256Hex(state.devClientSec), state.devBucket)
				if signedCode != http.StatusOK || signedBody != "signed-v4-ok" {
					t.Fatalf("expected keyless v4 signed URL 200 'signed-v4-ok', got %d %q", signedCode, signedBody)
				}
			})

			t.Run("when mutating notes and objects on preview-pr-12 and overlayfs.Store, then it reads dev state and isolates preview writes", func(t *testing.T) {
				// Arrange / Act.
				seedRead := getAppStorage(t, state.previewURL, "", "seed.txt")
				postAppNote(t, state.previewURL, "", "only-in-gcp-preview")
				postAppStorage(t, state.previewURL, "", "delta.txt", "preview-only-gcs")
				previewAfter := waitForAppHealth(t, state.previewURL, "")
				devAfter := waitForAppHealth(t, state.devURL, "")
				devDeltaStatus := getAppStorageStatus(t, state.devURL, "", "delta.txt")
				ofsReport := exerciseOverlayFS(t, state.devBucket, state.previewBucket, state.devBucket)
				h.WriteJSONArtifact(t, "isolation-report.json", map[string]any{
					"fallbackRead":   seedRead,
					"overlayClient":  ofsReport,
					"previewNotes":   previewAfter.DB.NoteCount,
					"devNotes":       devAfter.DB.NoteCount,
					"devDeltaStatus": devDeltaStatus,
				})

				// Assert.
				assertAppStorageAndDBIsolation(t, seedRead.SourceBucket, state.devBucket, seedRead.Content, "gcs-object-from-dev", previewAfter.DB.NoteCount, devAfter.DB.NoteCount, devDeltaStatus)
				if !ofsReport.DeltaAbsentInDev || ofsReport.DevSeedAfter != "gcs-object-from-dev" {
					t.Fatalf("expected overlayfs.Store to preserve dev bucket, got %+v", ofsReport)
				}
			})

			t.Run("when logging in and out via Kinde OIDC in headless Chrome, then both Cloud Run URLs complete end-to-end authentication", func(t *testing.T) {
				// Arrange / Act.
				hashLoginStatus, hashAuthLoc := callAppNoRedirect(t, state.previewHashURL+"/api/auth/login", "")
				hashKindeStatus, hashKindeLoc := followAuthorizeRedirect(t, hashAuthLoc)
				browserReport := verifyKindeBrowserLogin(t, h, state.previewURL, "")
				h.WriteJSONArtifact(t, "auth-login-flow.json", map[string]any{
					"hashUriLoginStatus": hashLoginStatus,
					"hashUriKindeStatus": hashKindeStatus,
					"browserLogin":       browserReport,
				})

				// Assert.
				assertAppLoginAndBrowserFlow(t, state.previewHashURL, state.devIssuer, hashLoginStatus, hashAuthLoc, hashKindeStatus, hashKindeLoc, browserReport)
			})

			t.Run("when destroying preview-pr-12 twice, then the preview Cloud Run service, GCS bucket, and Neon branch return 404 while dev, stage, and prod stay healthy", func(t *testing.T) {
				h.SkipIfKeep(t)
				// Arrange / Act.
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-12", "--yes", "--non-interactive", "--skip-preview")
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-12", "--yes", "--non-interactive", "--skip-preview")

				// Assert.
				assertCloudRunAbsent(t, gcpProject, state.previewSvc)
				assertServiceAccountAbsent(t, gcpProject, state.previewSA)
				assertBucketAbsent(t, state.previewBucket)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", state.neonProjectID, state.previewBranchID), http.StatusNotFound)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", state.neonProjectID, state.devBranchID), http.StatusOK)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", state.neonProjectID, state.stageBranchID), http.StatusOK)
			})
		})
	})
}

// Private helpers ------------------------------------------------------------

type gcpDeployedAppState struct {
	neonProjectID   string
	devIssuer       string
	devClientSec    string
	prodIssuer      string
	prodClientSec   string
	devURL          string
	prodURL         string
	stageURL        string
	stageDBURL      string
	previewURL      string
	previewHashURL  string
	devSvc          string
	prodSvc         string
	stageSvc        string
	previewSvc      string
	previewSA       string
	devBucket       string
	prodBucket      string
	stageBucket     string
	previewBucket   string
	devBranchID     string
	prodBranchID    string
	stageBranchID   string
	previewBranchID string
}

func deployAllGCPAppStacks(t *testing.T, h *LiveStackHarness) gcpDeployedAppState {
	t.Helper()
	h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
	s := gcpDeployedAppState{
		neonProjectID: h.Output(t, "shared", "neonProjectId"),
		devIssuer:     h.Output(t, "shared", "nonProdIssuerUrl"),
		devClientSec:  h.Output(t, "shared", "nonProdClientSecret"),
		prodIssuer:    h.Output(t, "shared", "prodIssuerUrl"),
		prodClientSec: h.Output(t, "shared", "prodClientSecret"),
	}

	h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
	s.devURL = h.Output(t, "dev", "regionalUrl")
	s.devSvc = h.Output(t, "dev", "serviceName")
	s.devBucket = h.Output(t, "dev", "bucketName")
	s.devBranchID = h.Output(t, "dev", "branchId")
	_ = waitForAppHealth(t, s.devURL, "")
	postAppNote(t, s.devURL, "", "seeded-in-gcp-dev")
	postAppStorage(t, s.devURL, "", "seed.txt", "gcs-object-from-dev")

	h.Pulumi(t, nil, "up", "--stack", "prod", "--yes", "--non-interactive", "--skip-preview")
	s.prodURL = h.Output(t, "prod", "regionalUrl")
	s.prodSvc = h.Output(t, "prod", "serviceName")
	s.prodBucket = h.Output(t, "prod", "bucketName")
	s.prodBranchID = h.Output(t, "prod", "branchId")
	_ = waitForAppHealth(t, s.prodURL, "")
	postAppNote(t, s.prodURL, "", "seeded-in-gcp-prod")

	h.Pulumi(t, nil, "up", "--stack", "stage", "--yes", "--non-interactive", "--skip-preview")
	s.stageURL = h.Output(t, "stage", "regionalUrl")
	s.stageSvc = h.Output(t, "stage", "serviceName")
	s.stageBucket = h.Output(t, "stage", "bucketName")
	s.stageBranchID = h.Output(t, "stage", "branchId")
	s.stageDBURL = h.Output(t, "stage", "databaseUrl")

	h.Pulumi(t, nil, "up", "--stack", "preview-pr-12", "--yes", "--non-interactive", "--skip-preview")
	s.previewURL = h.Output(t, "preview-pr-12", "regionalUrl")
	s.previewHashURL = h.Output(t, "preview-pr-12", "url")
	s.previewSvc = h.Output(t, "preview-pr-12", "serviceName")
	s.previewSA = h.Output(t, "preview-pr-12", "serviceAccountEmail")
	s.previewBucket = h.Output(t, "preview-pr-12", "bucketName")
	s.previewBranchID = h.Output(t, "preview-pr-12", "branchId")

	h.RecordLiveURL("dev", s.devURL)
	h.RecordLiveURL("prod", s.prodURL)
	h.RecordLiveURL("stage", s.stageURL)
	h.RecordLiveURL("preview-pr-12", s.previewURL)
	h.RecordLiveURL("preview-pr-12-uri", s.previewHashURL)
	return s
}

func verifyGCPProtectionUpdateAndFailure(t *testing.T, h *LiveStackHarness, s gcpDeployedAppState) {
	t.Helper()
	// Arrange / Act.
	prodMeta := describeGCSBucket(t, s.prodBucket)
	failProd := h.PulumiFail(t, nil, "destroy", "--stack", "prod", "--yes", "--non-interactive")
	failStage := h.PulumiFail(t, nil, "destroy", "--stack", "stage", "--yes", "--non-interactive")
	failApply := h.PulumiFail(t, []string{"OPENBASE_TEST_FAIL_INFRA=1"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
	h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=b"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
	devMeta := describeGCSBucket(t, s.devBucket)
	freshBucket := queryFreshCheckoutOutput(t, "gcp-neon-kinde-app", h.BackendURL, "dev", "bucketName")

	// Assert.
	assertProtectedBucketFields(t, s.prodBucket, prodMeta)
	assertContains(t, strings.ToLower(failProd), "protect")
	assertContains(t, strings.ToLower(failStage), "protect")
	assertContains(t, failApply, "simulated infrastructure program failure")
	assertEqualStr(t, "in-place updated revision label", devMeta.Labels["openbase-revision"], "b")
	assertEqualStr(t, "fresh checkout bucketName", freshBucket, s.devBucket)
	assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", s.neonProjectID, s.prodBranchID), http.StatusOK)
}

func queryFreshCheckoutOutput(t *testing.T, fixtureName, backendURL, stack, key string) string {
	t.Helper()
	freshDir := copyFixture(t, fixtureName)
	freshEnv := []string{
		"PULUMI_HOME=" + t.TempDir(),
		"PULUMI_BACKEND_URL=" + backendURL,
		"PULUMI_CONFIG_PASSPHRASE=",
		"PULUMI_SKIP_UPDATE_CHECK=true",
	}
	infraDir := filepath.Join(freshDir, "infra", "env")
	runCommandEnv(t, infraDir, freshEnv, "pulumi", "login", backendURL)
	return strings.TrimSpace(runCommandEnv(t, infraDir, freshEnv, "pulumi", "stack", "output", key, "--stack", stack))
}

func probeSignedEndpoint(t *testing.T, baseURL, bucketName string) (int, string) {
	t.Helper()
	seedGCSObject(t, bucketName, "signed-probe.txt", "signed-v4-ok")
	deadline := time.Now().Add(90 * time.Second)
	var lastCode int
	var lastBody string
	for time.Now().Before(deadline) {
		lastCode, lastBody = tryProbeSignedObject(baseURL + "/api/sign?key=signed-probe.txt")
		if lastCode == http.StatusOK {
			return lastCode, lastBody
		}
		time.Sleep(3 * time.Second)
	}
	return lastCode, lastBody
}

func tryProbeSignedObject(endpoint string) (int, string) {
	signedURL, msg := tryFetchSignedURL(endpoint)
	if signedURL == "" {
		return http.StatusInternalServerError, msg
	}
	resp, err := http.Get(signedURL)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func tryFetchSignedURL(endpoint string) (string, string) {
	raw, status, err := doAppRequest(http.MethodGet, endpoint, "", "", nil)
	if err != nil || status != http.StatusOK {
		return "", fmt.Sprintf("status=%d err=%v body=%s", status, err, string(raw))
	}
	var parsed struct {
		SignedURL string `json:"signedUrl"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.SignedURL == "" {
		return "", fmt.Sprintf("invalid signedUrl payload: %s", string(raw))
	}
	return parsed.SignedURL, ""
}

func assertPreviewGCPAppHealth(t *testing.T, health fullStackHealthResponse, dashboardHTML, devIssuer, devSecHash, devBucket string) {
	t.Helper()
	assertContains(t, dashboardHTML, "Sign in with Kinde OIDC")
	assertContains(t, dashboardHTML, "seeded-in-gcp-dev")
	assertEqualStr(t, "runtime", health.Runtime, "gcp-cloud-run")
	assertEqualStr(t, "environment", health.Environment, "preview-pr-12")
	assertEqualStr(t, "auth issuer", health.Auth.Issuer, devIssuer)
	assertEqualStr(t, "auth secret sha256", health.Auth.SecretSha256, devSecHash)
	assertEqualStr(t, "gcs fallback bucket", health.Storage.FallbackBucket, devBucket)
	if !health.DB.Connected || !health.DB.Pooled || health.DB.NoteCount != 1 {
		t.Fatalf("expected connected pooled Neon DB with 1 inherited note, got %+v", health.DB)
	}
}

func expectedGCPKindeURLs(tenant, appProject, projectNum string) ([]string, []string) {
	if tenant != "prod" {
		reg := fmt.Sprintf("https://%s-*-%s.us-central1.run.app", appProject, projectNum)
		hash := fmt.Sprintf("https://%s-*-uc.a.run.app", appProject)
		redirects := []string{
			"http://localhost:3000/api/auth/kinde_callback",
			"http://localhost:5173/api/auth/kinde_callback",
			hash + "/api/auth/kinde_callback",
			reg + "/api/auth/kinde_callback",
		}
		logouts := []string{
			"http://localhost:3000/",
			"http://localhost:5173/",
			hash + "/",
			reg + "/",
		}
		slices.Sort(redirects)
		slices.Sort(logouts)
		return redirects, logouts
	}
	stage := fmt.Sprintf("https://%s-stage-web-%s.us-central1.run.app", appProject, projectNum)
	prod := fmt.Sprintf("https://%s-prod-web-%s.us-central1.run.app", appProject, projectNum)
	stageHash := fmt.Sprintf("https://%s-stage-*-uc.a.run.app", appProject)
	prodHash := fmt.Sprintf("https://%s-prod-*-uc.a.run.app", appProject)
	redirects := []string{
		prod + "/api/auth/kinde_callback",
		prodHash + "/api/auth/kinde_callback",
		stage + "/api/auth/kinde_callback",
		stageHash + "/api/auth/kinde_callback",
	}
	logouts := []string{
		prod + "/",
		prodHash + "/",
		stage + "/",
		stageHash + "/",
	}
	slices.Sort(redirects)
	slices.Sort(logouts)
	return redirects, logouts
}

func assertGCPNeonKindeCleanup(t *testing.T, gcpProject, neonProjectID string, services, buckets []string) {
	t.Helper()
	assertCloudRunServicesAbsent(t, gcpProject, services...)
	assertBucketsAbsent(t, buckets...)
	if neonProjectID != "" {
		assertNeonStatus(t, "https://console.neon.tech/api/v2/projects/"+neonProjectID, http.StatusNotFound)
	}
	if countKindeAppsByPrefix(t, "non-prod", "openbase-e2e-gcp-") > 1 || countKindeAppsByPrefix(t, "prod", "openbase-e2e-gcp-") > 1 {
		t.Fatal("expected at most 1 retained openbase-e2e-gcp Kinde sandbox app per tenant")
	}
}
