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
	h := NewLiveHarness(
		t,
		"gcp-bucket",
		"openbase-gcp-bucket-fixture",
		backend,
		"OPENBASE_TEST_GCP_PROJECT="+gcpProject,
		"GOOGLE_PROJECT="+gcpProject,
		"OPENBASE_TEST_APP_PROJECT="+appProject,
	)
	h.InitStack(t, "dev")

	t.Run("given no infra exists", func(t *testing.T) {
		var bucketName string

		t.Run("when planning, then preview plans a private GCS bucket without creating cloud state", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "dev")
			h.WriteArtifact(t, "fresh-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "gcp:storage:Bucket")
		})

		t.Run("when applying", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			bucketName = h.Output(t, "dev", "bucketName")
			meta := describeGCSBucket(t, bucketName)
			seedGCSObject(t, bucketName, "probe.txt", "hello-gcs")

			// Assert.
			t.Run("then GCS enforces uniform bucket-level access, public access prevention, and OpenBase labels", func(t *testing.T) {
				require.True(t, meta.UniformBucketLevelAccess)
				require.Equal(t, "enforced", meta.PublicAccessPrevention)
				require.Equal(t, "dev", meta.Labels["openbase-environment"])
				require.Equal(t, "v1", meta.Labels["openbase-revision"])
			})
		})

		t.Run("when destroying, then the GCS bucket is deleted and returns not found", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			assertBucketAbsent(t, bucketName)
		})
	})

	t.Run("given prior infra exists", func(t *testing.T) {
		// Arrange (converged state).
		h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
		bucketName := h.Output(t, "dev", "bucketName")

		t.Run("when planning, then preview plans an in-place label update without replacing the bucket", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "preview", "--stack", "dev")
			h.WriteArtifact(t, "update-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "gcp:storage:Bucket")
			require.NotContains(t, planOut, "replace")
		})

		t.Run("when applying, then the existing GCS bucket updates its revision label in place", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			meta := describeGCSBucket(t, bucketName)

			// Assert.
			require.Equal(t, bucketName, h.Output(t, "dev", "bucketName"))
			require.Equal(t, "v2", meta.Labels["openbase-revision"])
		})

		t.Run("when destroying, then the updated GCS bucket is removed cleanly", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			assertBucketAbsent(t, bucketName)
		})
	})
}
