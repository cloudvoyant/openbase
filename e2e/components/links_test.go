package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
)

// Public API -----------------------------------------------------------------

func TestLinks(t *testing.T) {
	gcpProject, baseBackend := RequireLiveGCP(t)
	_ = RequireLiveNeon(t)
	_ = RequireLiveKinde(t)
	_ = RequireLiveVercel(t)

	orgID := detectNeonOrgID(t)
	teamID, teamSlug := detectVercelTeam(t)
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "oblnk" + runID
	devKindeID, devKindeName := prepareKindeSandboxApp(t, "dev", "openbase-e2e-"+appProject, appProject, teamSlug)
	prodKindeID, _ := prepareKindeSandboxApp(t, "prod", "openbase-e2e-"+appProject, appProject, teamSlug)

	h := NewLiveHarness(
		t,
		"links-fullstack",
		"openbase-links-fullstack-fixture",
		baseBackend,
		"OPENBASE_TEST_GCP_PROJECT="+gcpProject,
		"GOOGLE_PROJECT="+gcpProject,
		"VERCEL_API_TOKEN="+os.Getenv("VERCEL_TOKEN"),
		"NEON_ORG_ID="+orgID,
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_RUN_ID="+runID,
		"OPENBASE_TEST_VERCEL_TEAM_ID="+teamID,
		"OPENBASE_TEST_VERCEL_TEAM_SLUG="+teamSlug,
		"OPENBASE_TEST_KINDE_APP_NAME="+devKindeName,
		"OPENBASE_TEST_IMPORT_DEV_APP_ID="+devKindeID,
		"OPENBASE_TEST_IMPORT_PROD_APP_ID="+prodKindeID,
	)

	var vercelProjectID, neonProjectID, devBucket string
	h.OnCleanupVerify(func(t *testing.T) {
		assertFullstackCleanup(t, teamID, devBucket, neonProjectID, vercelProjectID)
	})

	t.Run("given a full-stack vercel.WebApp linked to internal gcp.CloudRunService, gcp.Bucket, neon.Database, and kinde.OidcApp", func(t *testing.T) {
		for _, s := range []string{"shared", "dev", "preview-pr-99"} {
			h.InitStack(t, s)
		}

		t.Run("given provisioned shared, seeded dev, and preview-pr-99 stacks", func(t *testing.T) {
			h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
			sharedEnv := collectSharedFullstackEnv(t, h)
			vercelProjectID = h.Output(t, "shared", "vercelProjectId")
			neonProjectID = h.Output(t, "shared", "neonProjectId")
			bypassSecret := h.Output(t, "shared", "protectionBypassSecret")
			expectedKindeHash := sha256Hex(h.Output(t, "shared", "devClientSecret"))

			h.Pulumi(t, sharedEnv, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			devBucket = h.Output(t, "dev", "bucketName")
			devBranchID := h.Output(t, "dev", "branchId")
			devDBURL := h.Output(t, "dev", "databaseUrl")
			seedDevGCSAndNeon(t, devBucket, devDBURL)

			previewEnv := append(append([]string{}, sharedEnv...), "OPENBASE_TEST_PARENT_BRANCH_ID="+devBranchID)
			h.Pulumi(t, previewEnv, "up", "--stack", "preview-pr-99", "--yes", "--non-interactive", "--skip-preview")

			webURL := h.Output(t, "preview-pr-99", "webUrl")
			h.RecordLiveURL("preview-pr-99", webURL)
			ordersURL := h.Output(t, "preview-pr-99", "ordersUrl")
			previewSvc := h.Output(t, "preview-pr-99", "ordersServiceName")
			previewBucket := h.Output(t, "preview-pr-99", "bucketName")
			previewBranchID := h.Output(t, "preview-pr-99", "branchId")
			previewDeployID := h.Output(t, "preview-pr-99", "webDeploymentId")
			previewDBURL := h.Output(t, "preview-pr-99", "databaseUrl")

			t.Run("when probing the deployed preview WebApp", func(t *testing.T) {
				// Arrange / Act.
				_, directOrdersStatus, _ := fetchProbe(ordersURL+"/health", "")
				cowRow := querySingleText(t, previewDBURL, "SELECT v FROM links_check LIMIT 1")
				payload := waitForFullstackLinksProbe(t, webURL+"/api/links", bypassSecret)
				h.WriteJSONArtifact(t, "fullstack-links-probe.json", payload)

				// Assert.
				t.Run("then direct unauthenticated Cloud Run requests return 403 while Vercel invokes Cloud Run via keyless GCP WIF OIDC", func(t *testing.T) {
					if directOrdersStatus != http.StatusForbidden {
						t.Fatalf("direct unauthenticated Cloud Run status = %d, want 403", directOrdersStatus)
					}
					assertEqualStr(t, "Cloud Run service", payload.CloudRun.Service, "orders")
				})

				t.Run("then Vercel and Cloud Run read seeded dev GCS/Neon data via BranchFrom overlays and receive matching Kinde secrets", func(t *testing.T) {
					assertEqualStr(t, "preview Neon CoW row", cowRow, "dev-row")
					if !payload.CloudRun.HasDatabaseURL || !payload.HasPooledDB || !payload.HasDirectDB {
						t.Fatalf("expected DB links in both Vercel and Cloud Run, got %+v", payload)
					}
					assertEqualStr(t, "GCS overlay fallback bucket", payload.GCSOverlay.SourceBucket, devBucket)
					assertEqualStr(t, "GCS overlay fallback payload", payload.GCSOverlay.Payload, "seeded-in-dev")
					assertEqualStr(t, "Vercel Kinde secret sha256", payload.KindeSecretSha256, expectedKindeHash)
					assertEqualStr(t, "Cloud Run Kinde secret sha256", payload.CloudRun.KindeSecretSha256, expectedKindeHash)
				})
			})

			t.Run("when destroying preview-pr-99 twice, then preview resources across GCP, Neon, and Vercel return 404", func(t *testing.T) {
				h.SkipIfKeep(t)
				// Arrange / Act.
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-99", "--yes", "--non-interactive", "--skip-preview")
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-99", "--yes", "--non-interactive", "--skip-preview")

				// Assert.
				assertCloudRunAbsent(t, gcpProject, previewSvc)
				assertBucketAbsent(t, previewBucket)
				assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, previewBranchID), http.StatusNotFound)
				assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v13/deployments/%s?teamId=%s", previewDeployID, teamID), http.StatusNotFound)
			})
		})
	})
}

// Private helpers ------------------------------------------------------------

type fullstackProbePayload struct {
	OK             bool `json:"ok"`
	CloudRunStatus int  `json:"cloudRunStatus"`
	CloudRun       struct {
		Service           string `json:"service"`
		HasDatabaseURL    bool   `json:"hasDatabaseUrl"`
		KindeSecretSha256 string `json:"kindeSecretSha256"`
	} `json:"cloudRun"`
	GCSOverlay struct {
		SourceBucket string `json:"sourceBucket"`
		Payload      string `json:"payload"`
	} `json:"gcsOverlay"`
	HasPooledDB       bool   `json:"hasPooledDb"`
	HasDirectDB       bool   `json:"hasDirectDb"`
	KindeSecretSha256 string `json:"kindeSecretSha256"`
	Error             string `json:"error"`
}

func assertFullstackCleanup(t *testing.T, teamID, devBucket, neonProjectID, vercelProjectID string) {
	t.Helper()
	if devBucket != "" {
		assertBucketAbsent(t, devBucket)
	}
	if neonProjectID != "" {
		assertNeonStatus(t, "https://console.neon.tech/api/v2/projects/"+neonProjectID, http.StatusNotFound)
	}
	if vercelProjectID != "" {
		assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v9/projects/%s?teamId=%s", vercelProjectID, teamID), http.StatusNotFound)
	}
}

func collectSharedFullstackEnv(t *testing.T, h *LiveStackHarness) []string {
	t.Helper()
	return []string{
		"OPENBASE_TEST_WIF_POOL_NAME=" + h.Output(t, "shared", "wifPoolName"),
		"OPENBASE_TEST_VERCEL_OIDC_PROVIDER=" + h.Output(t, "shared", "vercelOidcProviderName"),
		"OPENBASE_TEST_VERCEL_PROJECT_ID=" + h.Output(t, "shared", "vercelProjectId"),
		"OPENBASE_TEST_NEON_PROJECT_ID=" + h.Output(t, "shared", "neonProjectId"),
		"OPENBASE_TEST_DEFAULT_BRANCH_ID=" + h.Output(t, "shared", "neonDefaultBranchId"),
		"OPENBASE_TEST_DEV_ISSUER=" + h.Output(t, "shared", "devIssuerUrl"),
		"OPENBASE_TEST_DEV_CLIENT_ID=" + h.Output(t, "shared", "devClientId"),
		"OPENBASE_TEST_DEV_CLIENT_SECRET=" + h.Output(t, "shared", "devClientSecret"),
	}
}

func seedDevGCSAndNeon(t *testing.T, devBucket, devDBURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	wc := client.Bucket(devBucket).Object("seed.txt").NewWriter(ctx)
	if _, err := wc.Write([]byte("seeded-in-dev")); err != nil {
		t.Fatal(err)
	}
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}
	execSQL(t, devDBURL, "CREATE TABLE links_check (v TEXT); INSERT INTO links_check VALUES ('dev-row');")
}

func waitForFullstackLinksProbe(t *testing.T, url, bypassSecret string) fullstackProbePayload {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("x-vercel-protection-bypass", bypassSecret)
		resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
		if err == nil {
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			var p fullstackProbePayload
			if resp.StatusCode == http.StatusOK && json.Unmarshal(raw, &p) == nil && p.OK {
				return p
			}
			last = fmt.Sprintf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
		} else {
			last = err.Error()
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("GET %s timed out waiting for OIDC links probe: %s", url, last)
	return fullstackProbePayload{}
}
