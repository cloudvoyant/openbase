package e2e_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Public API -----------------------------------------------------------------

func TestGCPBucket(t *testing.T) {
	gcpProject, backend := RequireLiveGCP(t)
	appProject := fmt.Sprintf("obbkt%d", time.Now().Unix()%100000)
	var bucketName string

	runResourceLifecycleMatrix(t, resourceLifecycleSpec{
		Fixture:       "gcp-bucket",
		PulumiProject: "openbase-gcp-bucket-fixture",
		Backend:       backend,
		Env: []string{
			"OPENBASE_TEST_GCP_PROJECT=" + gcpProject,
			"GOOGLE_PROJECT=" + gcpProject,
			"OPENBASE_TEST_APP_PROJECT=" + appProject,
		},
		TargetStack: "dev",
		PlanToken:   "gcp:storage:Bucket",
		UpdateEnv:   []string{"OPENBASE_TEST_REVISION=v2"},
		AssertApplied: func(t *testing.T, h *LiveStackHarness, stack string) {
			bucketName = h.Output(t, stack, "bucketName")
			require.NotEmpty(t, bucketName)
			meta := describeGCSBucket(t, bucketName)
			require.Equal(t, "v1", meta.Labels["openbase-revision"])
		},
		AssertUpdated: func(t *testing.T, h *LiveStackHarness, stack string) {
			bucketName = h.Output(t, stack, "bucketName")
			meta := describeGCSBucket(t, bucketName)
			require.Equal(t, "v2", meta.Labels["openbase-revision"])
		},
		AssertAbsent: func(t *testing.T, _ *LiveStackHarness) {
			assertBucketAbsent(t, bucketName)
		},
	})
}
