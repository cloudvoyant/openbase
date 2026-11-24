package e2e_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Public API -----------------------------------------------------------------

func TestNeonDatabase(t *testing.T) {
	backend := RequireLiveNeon(t)
	neonOrgID := detectNeonOrgID(t)
	appProject := fmt.Sprintf("obdb%d", time.Now().Unix()%100000)

	runResourceLifecycleMatrix(t, resourceLifecycleSpec{
		Fixture:       "neon-database",
		PulumiProject: "openbase-neon-database-fixture",
		Backend:       backend,
		HasShared:     true,
		Env: []string{
			"NEON_ORG_ID=" + neonOrgID,
			"OPENBASE_TEST_APP_PROJECT=" + appProject,
		},
		TargetStack: "dev",
		PlanToken:   "neon:index:Branch",
		UpdateEnv:   []string{"OPENBASE_TEST_REVISION=v2"},
		AssertApplied: func(t *testing.T, h *LiveStackHarness, stack string) {
			require.Contains(t, h.Output(t, stack, "databaseUrl"), "postgres://")
			require.NotEmpty(t, h.Output(t, stack, "branchId"))
		},
		AssertUpdated: func(t *testing.T, h *LiveStackHarness, stack string) {
			require.Contains(t, h.Output(t, stack, "databaseUrl"), "postgres://")
		},
	})
}
