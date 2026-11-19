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

func TestLinks(t *testing.T) {
	gcpProject, baseBackend := RequireLiveGCP(t)
	_ = RequireLiveVercel(t)

	teamID, teamSlug := detectVercelTeam(t)
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "oblnk" + runID

	h := NewLiveHarness(
		t,
		"links-fullstack",
		"openbase-links-fullstack-fixture",
		baseBackend,
		"OPENBASE_TEST_GCP_PROJECT="+gcpProject,
		"GOOGLE_PROJECT="+gcpProject,
		"VERCEL_API_TOKEN="+os.Getenv("VERCEL_TOKEN"),
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_RUN_ID="+runID,
		"OPENBASE_TEST_VERCEL_TEAM_ID="+teamID,
		"OPENBASE_TEST_VERCEL_TEAM_SLUG="+teamSlug,
	)

	var vercelProjectID, sharedAssetsBucket, devBucket string
	h.OnCleanupVerify(func(t *testing.T) {
		assertHybridLinksCleanup(t, teamID, sharedAssetsBucket, devBucket, vercelProjectID)
	})

	t.Run("given separate openbase.Platform (platform), openbase.Project (infra/shared), and openbase.Environment (infra) hybrid Vercel + GCP programs", func(t *testing.T) {
		for _, s := range []string{"platform", "shared", "dev", "preview-pr-99"} {
			h.InitStack(t, s)
		}

		t.Run("given provisioned platform, shared (with shared-assets bucket), seeded dev, and preview-pr-99 stacks", func(t *testing.T) {
			h.Pulumi(t, nil, "up", "--stack", "platform", "--yes", "--non-interactive", "--skip-preview")
			h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
			vercelProjectID = h.Output(t, "shared", "vercelProjectId")
			bypassSecret := h.Output(t, "shared", "protectionBypassSecret")
			sharedAssetsBucket = h.Output(t, "shared", "bucketName")
			seedGCSObject(t, sharedAssetsBucket, "brand.txt", "shared-manifest-asset")

			h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			devBucket = h.Output(t, "dev", "bucketName")
			seedGCSObject(t, devBucket, "seed.txt", "seeded-in-dev")

			h.Pulumi(t, nil, "up", "--stack", "preview-pr-99", "--yes", "--non-interactive", "--skip-preview")
			webURL := h.Output(t, "preview-pr-99", "webUrl")
			h.RecordLiveURL("preview-pr-99", webURL)
			ordersURL := h.Output(t, "preview-pr-99", "ordersUrl")
			previewSvc := h.Output(t, "preview-pr-99", "ordersServiceName")
			previewBucket := h.Output(t, "preview-pr-99", "bucketName")
			previewDeployID := h.Output(t, "preview-pr-99", "webDeploymentId")

			t.Run("when probing the deployed preview WebApp", func(t *testing.T) {
				// Arrange / Act.
				_, directOrdersStatus, _ := doAppRequest(http.MethodGet, ordersURL+"/health", "", "", nil)
				payload := waitForHybridLinksProbe(t, webURL+"/api/links", bypassSecret)
				h.WriteJSONArtifact(t, "hybrid-links-probe.json", payload)

				// Assert.
				t.Run("then direct unauthenticated Cloud Run requests return 403 while Vercel invokes Cloud Run via keyless GCP WIF OIDC", func(t *testing.T) {
					if directOrdersStatus != http.StatusForbidden {
						t.Fatalf("direct unauthenticated Cloud Run status = %d, want 403", directOrdersStatus)
					}
					assertEqualStr(t, "Cloud Run service", payload.CloudRun.Service, "orders")
					assertEqualStr(t, "Cloud Run sharedAssetsBucket", payload.CloudRun.SharedAssetsBucket, sharedAssetsBucket)
				})

				t.Run("then Vercel reads both the dev BranchFrom overlay bucket and the remote shared-assets bucket from openbaseManifest", func(t *testing.T) {
					assertEqualStr(t, "GCS overlay fallback bucket", payload.GCSOverlay.SourceBucket, devBucket)
					assertEqualStr(t, "GCS overlay fallback payload", payload.GCSOverlay.Payload, "seeded-in-dev")
					assertEqualStr(t, "remote shared-assets bucket", payload.SharedAsset.SourceBucket, sharedAssetsBucket)
					assertEqualStr(t, "remote shared-assets payload", payload.SharedAsset.Payload, "shared-manifest-asset")
				})
			})

			t.Run("when destroying preview-pr-99 twice, then preview resources across GCP and Vercel return 404", func(t *testing.T) {
				h.SkipIfKeep(t)
				// Arrange / Act.
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-99", "--yes", "--non-interactive", "--skip-preview")
				h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-99", "--yes", "--non-interactive", "--skip-preview")

				// Assert.
				assertCloudRunAbsent(t, gcpProject, previewSvc)
				assertBucketAbsent(t, previewBucket)
				assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v13/deployments/%s?teamId=%s", previewDeployID, teamID), http.StatusNotFound)
			})
		})
	})
}

// Private helpers ------------------------------------------------------------

type hybridLinksProbePayload struct {
	OK             bool `json:"ok"`
	CloudRunStatus int  `json:"cloudRunStatus"`
	CloudRun       struct {
		Service            string `json:"service"`
		SharedAssetsBucket string `json:"sharedAssetsBucket"`
	} `json:"cloudRun"`
	GCSOverlay struct {
		SourceBucket string `json:"sourceBucket"`
		Payload      string `json:"payload"`
	} `json:"gcsOverlay"`
	SharedAsset struct {
		SourceBucket string `json:"sourceBucket"`
		Payload      string `json:"payload"`
	} `json:"sharedAsset"`
	Error string `json:"error"`
}

func assertHybridLinksCleanup(t *testing.T, teamID, sharedAssetsBucket, devBucket, vercelProjectID string) {
	t.Helper()
	assertBucketsAbsent(t, sharedAssetsBucket, devBucket)
	if vercelProjectID != "" {
		assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v9/projects/%s?teamId=%s", vercelProjectID, teamID), http.StatusNotFound)
	}
}

func waitForHybridLinksProbe(t *testing.T, targetURL, bypassSecret string) hybridLinksProbePayload {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, targetURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("x-vercel-protection-bypass", bypassSecret)
		resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
		if err == nil {
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			var p hybridLinksProbePayload
			if resp.StatusCode == http.StatusOK && json.Unmarshal(raw, &p) == nil && p.OK {
				return p
			}
			last = fmt.Sprintf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
		} else {
			last = err.Error()
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("GET %s timed out waiting for OIDC links probe: %s", targetURL, last)
	return hybridLinksProbePayload{}
}
