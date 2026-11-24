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

	runResourceLifecycleMatrix(t, resourceLifecycleSpec{
		Fixture:       "vercel-blob-store",
		PulumiProject: "openbase-vercel-blob-store-fixture",
		Backend:       backend,
		HasShared:     true,
		Env: []string{
			"VERCEL_API_TOKEN=" + os.Getenv("VERCEL_TOKEN"),
			"OPENBASE_TEST_APP_PROJECT=" + appProject,
			"OPENBASE_TEST_VERCEL_TEAM_ID=" + teamID,
			"OPENBASE_TEST_VERCEL_TEAM_SLUG=" + teamSlug,
		},
		TargetStack: "dev",
		PlanToken:   "openbase:vercel:BlobStore",
		UpdateEnv:   []string{"OPENBASE_TEST_REVISION=v2"},
		AssertApplied: func(t *testing.T, h *LiveStackHarness, stack string) {
			require.Equal(t, "dev/", h.Output(t, stack, "blobPrefix"))
		},
		AssertUpdated: func(t *testing.T, h *LiveStackHarness, stack string) {
			require.Equal(t, "prod/", h.Output(t, stack, "blobFallbackPrefix"))
		},
	})
}
