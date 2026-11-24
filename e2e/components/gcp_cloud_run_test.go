package e2e_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Public API -----------------------------------------------------------------

func TestGCPCloudRunService(t *testing.T) {
	gcpProject, backend := RequireLiveGCP(t)
	projectNumber := ResolveGCPProjectNumber(t, gcpProject)
	appProject := fmt.Sprintf("obcr%d", time.Now().Unix()%100000)
	h := NewLiveHarness(
		t,
		"gcp-cloud-run",
		"openbase-gcp-cloud-run-fixture",
		backend,
		"OPENBASE_TEST_GCP_PROJECT="+gcpProject,
		"GOOGLE_PROJECT="+gcpProject,
		"OPENBASE_TEST_GCP_PROJECT_NUMBER="+projectNumber,
		"OPENBASE_TEST_APP_PROJECT="+appProject,
	)
	h.InitStack(t, "dev")

	t.Run("given no infra exists", func(t *testing.T) {
		var svcName, saEmail string

		t.Run("when planning, then preview plans a Cloud Run v2 service and dedicated service account", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "dev")
			h.WriteArtifact(t, "fresh-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "gcp:cloudrunv2:Service")
			require.Contains(t, planOut, "gcp:serviceaccount:Account")
		})

		t.Run("when applying", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			svcName = h.Output(t, "dev", "serviceName")
			saEmail = h.Output(t, "dev", "serviceAccountEmail")
			svcURL := h.Output(t, "dev", "url")
			status := probeHTTPSStatus(t, svcURL)

			// Assert.
			t.Run("then Cloud Run serves HTTP 200 over HTTPS with a dedicated service account", func(t *testing.T) {
				require.NotEmpty(t, svcName)
				require.Contains(t, saEmail, "@"+gcpProject+".iam.gserviceaccount.com")
				require.Equal(t, http.StatusOK, status)
			})
		})

		t.Run("when destroying, then both the Cloud Run service and its service account are removed", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			assertCloudRunAbsent(t, gcpProject, svcName)
			assertServiceAccountAbsent(t, gcpProject, saEmail)
		})
	})

	t.Run("given prior infra exists", func(t *testing.T) {
		// Arrange (converged state).
		h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
		svcName := h.Output(t, "dev", "serviceName")
		saEmail := h.Output(t, "dev", "serviceAccountEmail")
		svcURL := h.Output(t, "dev", "url")

		t.Run("when planning, then preview plans an in-place OB_REVISION rollout on the existing service", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "preview", "--stack", "dev")
			h.WriteArtifact(t, "update-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "gcp:cloudrunv2:Service")
		})

		t.Run("when applying, then the service rolls out in place and continues serving HTTP 200 on the same URL", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			status := probeHTTPSStatus(t, svcURL)

			// Assert.
			require.Equal(t, svcName, h.Output(t, "dev", "serviceName"))
			require.Equal(t, http.StatusOK, status)
		})

		t.Run("when destroying, then the Cloud Run service and service account are deleted cleanly", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			assertCloudRunAbsent(t, gcpProject, svcName)
			assertServiceAccountAbsent(t, gcpProject, saEmail)
		})
	})
}

// Private helpers ------------------------------------------------------------

func probeHTTPSStatus(t *testing.T, targetURL string) int {
	t.Helper()
	client := &http.Client{Timeout: 20 * time.Second}
	var lastStatus int
	for range 6 {
		resp, err := client.Get(targetURL)
		if err == nil {
			lastStatus = resp.StatusCode
			_ = resp.Body.Close()
			if lastStatus == http.StatusOK {
				return lastStatus
			}
		}
		time.Sleep(3 * time.Second)
	}
	return lastStatus
}
