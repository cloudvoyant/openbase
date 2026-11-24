package e2e_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Public API -----------------------------------------------------------------

func TestVercelBlobStore(t *testing.T) {
	backend := RequireLiveVercel(t)
	teamID, teamSlug := detectVercelTeam(t)
	appProject := fmt.Sprintf("obvb%d", time.Now().Unix()%100000)
	h := NewLiveHarness(
		t,
		"vercel-blob-store",
		"openbase-vercel-blob-store-fixture",
		backend,
		"VERCEL_API_TOKEN="+os.Getenv("VERCEL_TOKEN"),
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_VERCEL_TEAM_ID="+teamID,
		"OPENBASE_TEST_VERCEL_TEAM_SLUG="+teamSlug,
	)
	h.InitStack(t, "shared")
	h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
	vercelProjectID := h.Output(t, "shared", "vercelProjectId")
	blobRWToken := fetchProjectBlobRWToken(t, teamID, vercelProjectID)
	h.InitStack(t, "dev")

	t.Run("given no infra exists", func(t *testing.T) {
		t.Run("when planning, then preview plans the Vercel BlobStore component", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "dev")
			h.WriteArtifact(t, "fresh-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "openbase:vercel:BlobStore")
		})

		t.Run("when applying", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			baseURL := h.Output(t, "dev", "blobBaseUrl")
			prefix := h.Output(t, "dev", "blobPrefix")
			src, seedVal, isolated := exerciseBlobOverlayClient(t, baseURL, blobRWToken, prefix, "preview-probe/", prefix)

			// Assert.
			t.Run("then BlobOverlayClient reads and isolates objects under the provisioned dev/ prefix", func(t *testing.T) {
				require.Equal(t, "dev/", prefix)
				require.Contains(t, baseURL, ".public.blob.vercel-storage.com")
				require.Equal(t, "fallback", src)
				require.Equal(t, "overlay-seed", seedVal)
				require.True(t, isolated)
			})
		})

		t.Run("when destroying, then the dev BlobStore stack tears down cleanly", func(t *testing.T) {
			// Arrange / Act / Assert.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
		})
	})

	t.Run("given prior infra exists", func(t *testing.T) {
		// Arrange (converged state).
		h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

		t.Run("when planning, then preview plans the updated BranchFrom fallback prefix", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "preview", "--stack", "dev")
			h.WriteArtifact(t, "update-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "openbase:vercel:BlobStore")
		})

		t.Run("when applying, then blobFallbackPrefix updates in place to prod/", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			require.Equal(t, "prod/", h.Output(t, "dev", "blobFallbackPrefix"))
		})

		t.Run("when destroying, then the updated BlobStore stack tears down cleanly", func(t *testing.T) {
			// Arrange / Act / Assert.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
		})
	})
}
