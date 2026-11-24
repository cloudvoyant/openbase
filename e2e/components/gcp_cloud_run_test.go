package e2e_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Public API -----------------------------------------------------------------

func TestGCPCloudRunService(t *testing.T) {
	gcpProject, backend := RequireLiveGCP(t)
	projectNumber := ResolveGCPProjectNumber(t, gcpProject)
	appProject := fmt.Sprintf("obcr%d", time.Now().Unix()%100000)
	var svcName string

	runResourceLifecycleMatrix(t, resourceLifecycleSpec{
		Fixture:       "gcp-cloud-run",
		PulumiProject: "openbase-gcp-cloud-run-fixture",
		Backend:       backend,
		Env: []string{
			"OPENBASE_TEST_GCP_PROJECT=" + gcpProject,
			"GOOGLE_PROJECT=" + gcpProject,
			"OPENBASE_TEST_GCP_PROJECT_NUMBER=" + projectNumber,
			"OPENBASE_TEST_APP_PROJECT=" + appProject,
		},
		TargetStack: "dev",
		PlanToken:   "gcp:cloudrunv2:Service",
		UpdateEnv:   []string{"OPENBASE_TEST_REVISION=v2"},
		AssertApplied: func(t *testing.T, h *LiveStackHarness, stack string) {
			svcName = h.Output(t, stack, "serviceName")
			require.NotEmpty(t, svcName)
			require.Contains(t, h.Output(t, stack, "url"), "https://")
		},
		AssertUpdated: func(t *testing.T, h *LiveStackHarness, stack string) {
			svcName = h.Output(t, stack, "serviceName")
			require.Contains(t, h.Output(t, stack, "url"), "https://")
		},
		AssertAbsent: func(t *testing.T, _ *LiveStackHarness) {
			assertCloudRunAbsent(t, gcpProject, svcName)
		},
	})
}
