package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
)

// Public API -----------------------------------------------------------------

func TestGCPCloudRunService(t *testing.T) {
	gcpProject, baseBackend := RequireLiveGCP(t)
	projectNumber := strings.TrimSpace(runCommand(t, repositoryRoot(t), "gcloud", "projects", "describe", gcpProject, "--format=value(projectNumber)"))
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "obcr" + runID
	secretPlain := "openbase-e2e-linked-secret-" + runID
	expectedSecretHash := sha256Hex(secretPlain)

	h := NewLiveHarness(
		t,
		"gcp-cloud-run",
		"openbase-gcp-cloud-run-fixture",
		baseBackend,
		"OPENBASE_TEST_GCP_PROJECT="+gcpProject,
		"GOOGLE_PROJECT="+gcpProject,
		"OPENBASE_TEST_GCP_PROJECT_NUMBER="+projectNumber,
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_RUN_ID="+runID,
		"OPENBASE_TEST_SECRET_VALUE="+secretPlain,
	)

	longPreviewStack := "preview-feature-extremely-long-branch-slug-" + runID
	var previewPubSvc, previewSAEmail, devPubSvc, prodPubSvc string
	h.OnCleanupVerify(func(t *testing.T) {
		assertCloudRunServicesAbsent(t, gcpProject, previewPubSvc, devPubSvc, prodPubSvc)
	})

	t.Run("given a gcp.Shared and public/internal gcp.CloudRunService program", func(t *testing.T) {
		for _, stack := range []string{"shared", "dev", "prod", longPreviewStack} {
			h.InitStack(t, stack)
		}

		t.Run("when previewing a long preview branch, then it plans gcp:cloudrunv2:Service without mutating cloud state", func(t *testing.T) {
			// Arrange / Act.
			previewPlan := h.Pulumi(t, nil, "preview", "--stack", longPreviewStack)
			h.WriteArtifact(t, "long-preview-plan.txt", previewPlan)

			// Assert.
			assertContains(t, previewPlan, "gcp:cloudrunv2:Service")
		})

		t.Run("given provisioned shared, dev, prod, and long-preview stacks", func(t *testing.T) {
			h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
			repoID := h.Output(t, "shared", "repositoryId")
			envRevA := []string{"OPENBASE_TEST_AR_REPO_ID=" + repoID, "OPENBASE_TEST_REVISION=a"}
			for _, stack := range []string{"dev", "prod", longPreviewStack} {
				h.Pulumi(t, envRevA, "up", "--stack", stack, "--yes", "--non-interactive", "--skip-preview")
			}
			previewPubURL := h.Output(t, longPreviewStack, "publicUrl")
			previewIntURL := h.Output(t, longPreviewStack, "internalUrl")
			previewSAID := h.Output(t, longPreviewStack, "publicSAId")
			previewSAEmail = h.Output(t, longPreviewStack, "publicSAEmail")
			previewPubSvc = h.Output(t, longPreviewStack, "publicServiceName")
			previewBucket := h.Output(t, longPreviewStack, "bucketName")
			devPubSvc = h.Output(t, "dev", "publicServiceName")
			prodPubSvc = h.Output(t, "prod", "publicServiceName")

			t.Run("when updating the preview service in place, then it bounds the SA ID to 6-30 chars and serves the new revision", func(t *testing.T) {
				// Arrange.
				envRevB := []string{"OPENBASE_TEST_AR_REPO_ID=" + repoID, "OPENBASE_TEST_REVISION=b"}

				// Act.
				h.Pulumi(t, envRevB, "up", "--stack", longPreviewStack, "--yes", "--non-interactive", "--skip-preview")
				body := waitForJSONProbe(t, previewPubURL+"/health", "")

				// Assert.
				if len(previewSAID) < 6 || len(previewSAID) > 30 {
					t.Fatalf("preview SA ID %q len=%d outside [6,30]", previewSAID, len(previewSAID))
				}
				assertEqualStr(t, "in-place revision", body.Revision, "b")
			})

			t.Run("when probing public and internal endpoints", func(t *testing.T) {
				// Arrange.
				idToken := strings.TrimSpace(runCommand(t, repositoryRoot(t), "gcloud", "auth", "print-identity-token"))
				h.RegisterSecret(idToken)

				// Act.
				pubHealth := waitForJSONProbe(t, previewPubURL+"/health", "")
				_, unauthIntStatus, _ := fetchProbe(previewIntURL+"/health", "")
				intHealth := waitForJSONProbe(t, previewIntURL+"/health", idToken)
				linkedCall := waitForJSONProbe(t, previewPubURL+"/call-internal", "")
				signedStatus, signedPayload := probeKeylessSignedURL(t, previewBucket, previewPubURL)

				h.WriteJSONArtifact(t, "public-health.json", pubHealth)
				h.WriteJSONArtifact(t, "internal-auth-health.json", intHealth)
				h.WriteJSONArtifact(t, "linked-call-internal.json", linkedCall)
				h.WriteJSONArtifact(t, "signed-url-probe.json", map[string]any{"status": signedStatus, "body": signedPayload})

				// Assert.
				t.Run("then public serves HTTP 200 unauthenticated with mounted Secret Manager secrets", func(t *testing.T) {
					assertEqualStr(t, "mounted secret sha256", pubHealth.SecretSha256, expectedSecretHash)
				})

				t.Run("then internal rejects unauthenticated callers (403) and serves 200 with IAM ID tokens or linked services", func(t *testing.T) {
					if unauthIntStatus != http.StatusForbidden {
						t.Fatalf("unauthenticated internal status = %d, want 403", unauthIntStatus)
					}
					assertEqualStr(t, "internal authenticated revision", intHealth.Revision, "b")
					assertEqualStr(t, "linked service-to-service revision", linkedCall.Upstream.Revision, "b")
				})

				t.Run("then the service signs a working GCS v4 URL via tokenCreator without static JSON keys", func(t *testing.T) {
					if signedStatus != http.StatusOK {
						t.Fatalf("signed URL status = %d, want 200", signedStatus)
					}
					assertEqualStr(t, "signed URL payload", signedPayload, "keyless-v4-ok")
				})
			})

			t.Run("when destroying the preview stack twice, then it deletes the Cloud Run service and SA (404) idempotently", func(t *testing.T) {
				// Arrange / Act.
				h.Pulumi(t, nil, "destroy", "--stack", longPreviewStack, "--yes", "--non-interactive", "--skip-preview")
				h.Pulumi(t, nil, "destroy", "--stack", longPreviewStack, "--yes", "--non-interactive", "--skip-preview")

				// Assert.
				assertCloudRunAbsent(t, gcpProject, previewPubSvc)
				assertServiceAccountAbsent(t, gcpProject, previewSAEmail)
			})
		})
	})
}

// Private helpers ------------------------------------------------------------

type cloudRunProbePayload struct {
	OK           bool   `json:"ok"`
	Revision     string `json:"revision"`
	SecretSha256 string `json:"secretSha256"`
	SignedURL    string `json:"signedUrl"`
	Upstream     struct {
		Revision string `json:"revision"`
	} `json:"upstream"`
}

func assertCloudRunServicesAbsent(t *testing.T, gcpProject string, names ...string) {
	t.Helper()
	for _, svc := range names {
		if svc != "" {
			assertCloudRunAbsent(t, gcpProject, svc)
		}
	}
}

func waitForJSONProbe(t *testing.T, url, bearer string) cloudRunProbePayload {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		payload, status, err := fetchProbe(url, bearer)
		if err == nil && status == http.StatusOK {
			return payload
		}
		lastErr = fmt.Errorf("status=%d err=%v", status, err)
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("GET %s timed out waiting for 200 OK: %v", url, lastErr)
	return cloudRunProbePayload{}
}

func fetchProbe(url, bearer string) (cloudRunProbePayload, int, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return cloudRunProbePayload{}, 0, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return cloudRunProbePayload{}, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return cloudRunProbePayload{}, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return cloudRunProbePayload{}, resp.StatusCode, fmt.Errorf("body=%s", string(raw))
	}
	var p cloudRunProbePayload
	return p, resp.StatusCode, json.Unmarshal(raw, &p)
}

func probeKeylessSignedURL(t *testing.T, bucketName, publicURL string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	wc := client.Bucket(bucketName).Object("signed-probe.txt").NewWriter(ctx)
	if _, err := wc.Write([]byte("keyless-v4-ok")); err != nil {
		t.Fatal(err)
	}
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}

	signPayload := waitForJSONProbe(t, publicURL+"/sign?object=signed-probe.txt", "")
	if signPayload.SignedURL == "" {
		t.Fatal("expected non-empty signedUrl from /sign")
	}
	resp, err := http.Get(signPayload.SignedURL)
	if err != nil {
		t.Fatalf("GET signedUrl: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func assertCloudRunAbsent(t *testing.T, gcpProject, serviceName string) {
	t.Helper()
	out := commandFailureEnv(
		t,
		repositoryRoot(t),
		nil,
		"gcloud", "run", "services", "describe", serviceName,
		"--region=us-central1",
		"--project="+gcpProject,
	)
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "cannot find") && !strings.Contains(lower, "not found") && !strings.Contains(lower, "404") {
		t.Fatalf("expected Cloud Run service %s absent, got:\n%s", serviceName, out)
	}
}

func assertServiceAccountAbsent(t *testing.T, gcpProject, email string) {
	t.Helper()
	listed := strings.TrimSpace(runCommand(
		t,
		repositoryRoot(t),
		"gcloud", "iam", "service-accounts", "list",
		"--project="+gcpProject,
		"--filter=email="+email,
		"--format=value(email)",
	))
	if listed != "" {
		t.Fatalf("expected SA %s deleted, still listed: %s", email, listed)
	}
	out := commandFailureEnv(
		t,
		repositoryRoot(t),
		nil,
		"gcloud", "iam", "service-accounts", "describe", email,
		"--project="+gcpProject,
	)
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "not found") && !strings.Contains(lower, "may not exist") && !strings.Contains(lower, "404") {
		t.Fatalf("expected SA %s absent, got:\n%s", email, out)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
