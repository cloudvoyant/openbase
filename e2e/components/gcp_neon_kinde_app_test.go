package e2e_test

import (
	"fmt"
	"net/http"
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

	projectNumber := strings.TrimSpace(runCommand(t, repositoryRoot(t), "gcloud", "projects", "describe", gcpProject, "--format=value(projectNumber)"))
	neonOrgID := detectNeonOrgID(t)
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "obga" + runID
	devR, devL := expectedGCPKindeURLs("dev", appProject, projectNumber)
	prodR, prodL := expectedGCPKindeURLs("prod", appProject, projectNumber)
	devAppID, devAppName := prepareKindeSandboxWithURLs(t, "dev", appProject, devR, devL)
	prodAppID, _ := prepareKindeSandboxWithURLs(t, "prod", appProject, prodR, prodL)

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
		"OPENBASE_TEST_KINDE_APP_NAME="+devAppName,
		"OPENBASE_TEST_IMPORT_DEV_APP_ID="+devAppID,
		"OPENBASE_TEST_IMPORT_PROD_APP_ID="+prodAppID,
		"OPENBASE_TEST_RETAIN_KINDE_APPS=1",
	)

	var devSvc, previewSvc, devSA, previewSA, devBucket, previewBucket, neonProjectID string
	h.OnCleanupVerify(func(t *testing.T) {
		assertGCPNeonKindeCleanup(t, gcpProject, neonProjectID, []string{devSvc, previewSvc}, []string{devBucket, previewBucket})
	})

	t.Run("given a gcp-neon-kinde-app consumer module", func(t *testing.T) {
		for _, stack := range []string{"shared", "dev", "preview-pr-12"} {
			h.InitStack(t, stack)
		}

		t.Run("when previewing shared, then it plans GCP, Neon, and Kinde shared resources without mutating cloud state", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "shared")
			h.WriteArtifact(t, "shared-preview-plan.txt", planOut)

			// Assert.
			assertContains(t, planOut, "gcp:artifactregistry:Repository")
			assertContains(t, planOut, "neon:index:Project")
			assertContains(t, planOut, "kinde:index:Application")
		})

		t.Run("given provisioned shared, seeded dev, and preview-pr-12 stacks wired via StackReferences", func(t *testing.T) {
			h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
			neonProjectID = h.Output(t, "shared", "neonProjectId")
			devIssuer := h.Output(t, "shared", "devIssuerUrl")
			devClientSec := h.Output(t, "shared", "devClientSecret")

			h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			devURL := h.Output(t, "dev", "regionalUrl")
			devSvc = h.Output(t, "dev", "serviceName")
			devSA = h.Output(t, "dev", "serviceAccountEmail")
			devBucket = h.Output(t, "dev", "bucketName")
			devBranchID := h.Output(t, "dev", "branchId")
			h.RecordLiveURL("dev", devURL)
			_ = devSA
			_ = waitForAppHealth(t, devURL, "")
			postAppNote(t, devURL, "", "seeded-in-gcp-dev")
			postAppStorage(t, devURL, "", "seed.txt", "gcs-object-from-dev")

			h.Pulumi(t, nil, "up", "--stack", "preview-pr-12", "--yes", "--non-interactive", "--skip-preview")
			previewURL := h.Output(t, "preview-pr-12", "regionalUrl")
			previewSvc = h.Output(t, "preview-pr-12", "serviceName")
			previewSA = h.Output(t, "preview-pr-12", "serviceAccountEmail")
			previewBucket = h.Output(t, "preview-pr-12", "bucketName")
			previewBranchID := h.Output(t, "preview-pr-12", "branchId")
			h.RecordLiveURL("preview-pr-12", previewURL)

			t.Run("when probing / and /api/health on preview-pr-12, then it serves the HTML dashboard and verifies Neon, GCS, and Secret Manager Kinde wiring", func(t *testing.T) {
				// Arrange / Act.
				health := waitForAppHealth(t, previewURL, "")
				dashboardHTML := fetchAppText(t, previewURL+"/", "")
				h.WriteJSONArtifact(t, "preview-health.json", health)

				// Assert.
				assertContains(t, dashboardHTML, "Sign in with Kinde OIDC")
				assertContains(t, dashboardHTML, "seeded-in-gcp-dev")
				assertEqualStr(t, "runtime", health.Runtime, "gcp-cloud-run")
				assertEqualStr(t, "environment", health.Environment, "preview-pr-12")
				assertEqualStr(t, "auth issuer", health.Auth.Issuer, devIssuer)
				assertEqualStr(t, "auth secret sha256", health.Auth.SecretSha256, sha256Hex(devClientSec))
				assertEqualStr(t, "gcs fallback bucket", health.Storage.FallbackBucket, devBucket)
				if !health.DB.Connected || !health.DB.Pooled || health.DB.NoteCount != 1 {
					t.Fatalf("expected connected pooled Neon DB with 1 inherited note, got %+v", health.DB)
				}
			})

			t.Run("when mutating notes and objects on preview-pr-12, then it reads dev state via CoW/overlay and isolates preview writes from dev", func(t *testing.T) {
				// Arrange / Act.
				seedRead := getAppStorage(t, previewURL, "", "seed.txt")
				postAppNote(t, previewURL, "", "only-in-gcp-preview")
				postAppStorage(t, previewURL, "", "delta.txt", "preview-only-gcs")
				previewAfter := waitForAppHealth(t, previewURL, "")
				devAfter := waitForAppHealth(t, devURL, "")
				devDeltaStatus := getAppStorageStatus(t, devURL, "", "delta.txt")
				h.WriteJSONArtifact(t, "isolation-report.json", map[string]any{
					"fallbackRead":   seedRead,
					"previewNotes":   previewAfter.DB.NoteCount,
					"devNotes":       devAfter.DB.NoteCount,
					"devDeltaStatus": devDeltaStatus,
				})

				// Assert.
				assertEqualStr(t, "fallback sourceBucket", seedRead.SourceBucket, devBucket)
				assertEqualStr(t, "fallback content", seedRead.Content, "gcs-object-from-dev")
				if previewAfter.DB.NoteCount != 2 || devAfter.DB.NoteCount != 1 {
					t.Fatalf("expected preview notes=2 and dev notes=1, got preview=%d dev=%d", previewAfter.DB.NoteCount, devAfter.DB.NoteCount)
				}
				if devDeltaStatus != http.StatusNotFound {
					t.Fatalf("expected delta.txt 404 on dev, got %d", devDeltaStatus)
				}
			})

			t.Run("when initiating /api/auth/login on preview-pr-12, then it redirects to Kinde OIDC and Kinde accepts the Cloud Run callback URL", func(t *testing.T) {
				// Arrange / Act.
				loginStatus, authLoc := callAppNoRedirect(t, previewURL+"/api/auth/login", "")
				kindeStatus, kindeLoc := followAuthorizeRedirect(t, authLoc)
				h.WriteJSONArtifact(t, "auth-login-flow.json", map[string]any{
					"appLoginStatus":  loginStatus,
					"authorizeUrl":    authLoc,
					"kindeAuthStatus": kindeStatus,
					"kindeLoginLoc":   kindeLoc,
				})

				// Assert.
				if loginStatus != http.StatusFound {
					t.Fatalf("expected 302 from /api/auth/login, got %d", loginStatus)
				}
				assertContains(t, authLoc, devIssuer+"/oauth2/auth")
				assertLoginRedirect(t, authLoc, kindeStatus, kindeLoc)
			})

			t.Run("when destroying preview-pr-12 twice, then the preview Cloud Run service, GCS bucket, and Neon branch return 404 while dev stays healthy", func(t *testing.T) {
				h.SkipIfKeep(t)
				// Arrange / Act.
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-12", "--yes", "--non-interactive", "--skip-preview")
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-12", "--yes", "--non-interactive", "--skip-preview")

				// Assert.
				assertCloudRunAbsent(t, gcpProject, previewSvc)
				assertServiceAccountAbsent(t, gcpProject, previewSA)
				assertBucketAbsent(t, previewBucket)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, previewBranchID), http.StatusNotFound)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, devBranchID), http.StatusOK)
			})
		})
	})
}

// Private helpers ------------------------------------------------------------

func expectedGCPKindeURLs(tenant, appProject, projectNum string) ([]string, []string) {
	if tenant == "dev" {
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
	redirects := []string{prod + "/api/auth/kinde_callback", stage + "/api/auth/kinde_callback"}
	logouts := []string{prod + "/", stage + "/"}
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
	if countOpenBaseKindeApps(t, "dev") > 1 || countOpenBaseKindeApps(t, "prod") > 1 {
		t.Fatal("expected at most 1 retained openbase-e2e Kinde sandbox app per tenant")
	}
}
