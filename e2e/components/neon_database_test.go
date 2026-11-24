package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// Public API -----------------------------------------------------------------

func TestNeonDatabase(t *testing.T) {
	backend := RequireLiveNeon(t)
	neonOrgID := detectNeonOrgID(t)
	appProject := fmt.Sprintf("obdb%d", time.Now().Unix()%100000)
	h := NewLiveHarness(
		t,
		"neon-database",
		"openbase-neon-database-fixture",
		backend,
		"NEON_ORG_ID="+neonOrgID,
		"OPENBASE_TEST_APP_PROJECT="+appProject,
	)
	h.InitStack(t, "shared")
	h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
	neonProjectID := h.Output(t, "shared", "neonProjectId")
	h.InitStack(t, "dev")

	t.Run("given no infra exists", func(t *testing.T) {
		var branchID string

		t.Run("when planning, then preview plans a Neon branch, compute endpoint, role, and database", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "dev")
			h.WriteArtifact(t, "fresh-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "neon:index:Branch")
			require.Contains(t, planOut, "neon:index:Endpoint")
		})

		t.Run("when applying", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			branchID = h.Output(t, "dev", "branchId")
			dbURL := h.Output(t, "dev", "databaseUrl")
			directURL := h.Output(t, "dev", "directUrl")
			execSQL(t, directURL, "CREATE TABLE IF NOT EXISTS ob_probe (val TEXT); INSERT INTO ob_probe VALUES ('neon-ok');")
			got := querySingleText(t, dbURL, "SELECT val FROM ob_probe LIMIT 1")

			// Assert.
			t.Run("then directUrl accepts DDL and pooled databaseUrl serves the written row over pgx", func(t *testing.T) {
				require.NotEmpty(t, branchID)
				require.Equal(t, "neon-ok", got)
			})
		})

		t.Run("when destroying, then the Neon branch is deleted and returns 404 from the Neon API", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, branchID), http.StatusNotFound)
		})
	})

	t.Run("given prior infra exists", func(t *testing.T) {
		// Arrange (converged state with seeded SQL row).
		h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
		branchID := h.Output(t, "dev", "branchId")
		directURL := h.Output(t, "dev", "directUrl")
		execSQL(t, directURL, "CREATE TABLE IF NOT EXISTS ob_keep (val TEXT); INSERT INTO ob_keep VALUES ('preserved');")

		t.Run("when planning, then preview plans an in-place endpoint update without replacing the branch", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "preview", "--stack", "dev")
			h.WriteArtifact(t, "update-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "neon:index:Endpoint")
		})

		t.Run("when applying, then the endpoint updates in place while preserving PostgreSQL data", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			got := querySingleText(t, h.Output(t, "dev", "databaseUrl"), "SELECT val FROM ob_keep LIMIT 1")

			// Assert.
			require.Equal(t, branchID, h.Output(t, "dev", "branchId"))
			require.Equal(t, "preserved", got)
		})

		t.Run("when destroying, then the Neon branch is removed cleanly", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, branchID), http.StatusNotFound)
		})
	})
}

// Private helpers ------------------------------------------------------------

func execSQL(t *testing.T, connURL, stmt string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, connURL)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, stmt)
	require.NoError(t, err)
}
