package e2e_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Public API -----------------------------------------------------------------

func TestVercelWebApp(t *testing.T) {
	backend := RequireLiveVercel(t)
	teamID, teamSlug := detectVercelTeam(t)
	appProject := fmt.Sprintf("obvw%d", time.Now().Unix()%100000)

	runResourceLifecycleMatrix(t, resourceLifecycleSpec{
		Fixture:       "vercel-webapp",
		PulumiProject: "openbase-vercel-webapp-fixture",
		Backend:       backend,
		HasShared:     true,
		Env: []string{
			"VERCEL_API_TOKEN=" + os.Getenv("VERCEL_TOKEN"),
			"OPENBASE_TEST_APP_PROJECT=" + appProject,
			"OPENBASE_TEST_VERCEL_TEAM_ID=" + teamID,
			"OPENBASE_TEST_VERCEL_TEAM_SLUG=" + teamSlug,
		},
		TargetStack: "dev",
		PlanToken:   "vercel:index:Deployment",
		UpdateEnv:   []string{"OPENBASE_TEST_REVISION=v2"},
		AssertApplied: func(t *testing.T, h *LiveStackHarness, stack string) {
			require.Contains(t, h.Output(t, stack, "url"), "https://")
		},
		AssertUpdated: func(t *testing.T, h *LiveStackHarness, stack string) {
			require.Contains(t, h.Output(t, stack, "url"), "https://")
		},
	})
}
