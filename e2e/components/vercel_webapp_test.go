package e2e_test

import (
	"fmt"
	"net/http"
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
	h := NewLiveHarness(
		t,
		"vercel-webapp",
		"openbase-vercel-webapp-fixture",
		backend,
		"VERCEL_API_TOKEN="+os.Getenv("VERCEL_TOKEN"),
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_VERCEL_TEAM_ID="+teamID,
		"OPENBASE_TEST_VERCEL_TEAM_SLUG="+teamSlug,
	)
	h.InitStack(t, "shared")
	h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
	bypassSecret := h.Output(t, "shared", "protectionBypassSecret")
	h.InitStack(t, "dev")

	t.Run("given no infra exists", func(t *testing.T) {
		var deploymentID string

		t.Run("when planning, then preview plans a Vercel Deployment and Alias", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "dev")
			h.WriteArtifact(t, "fresh-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "vercel:index:Deployment")
			require.Contains(t, planOut, "vercel:index:Alias")
		})

		t.Run("when applying", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			deploymentID = h.Output(t, "dev", "deploymentId")
			appURL := h.Output(t, "dev", "url")
			body := fetchAppText(t, appURL, bypassSecret)

			// Assert.
			t.Run("then the Vercel alias serves the deployed fixture HTML over HTTPS", func(t *testing.T) {
				require.NotEmpty(t, deploymentID)
				require.Contains(t, body, "openbase vercel webapp fixture")
			})
		})

		t.Run("when destroying, then the Vercel deployment is removed and returns 404", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v13/deployments/%s?teamId=%s", deploymentID, teamID), http.StatusNotFound)
		})
	})

	t.Run("given prior infra exists", func(t *testing.T) {
		// Arrange (converged state).
		h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
		appURL := h.Output(t, "dev", "url")

		t.Run("when planning, then preview plans the OB_REVISION deployment update", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "preview", "--stack", "dev")
			h.WriteArtifact(t, "update-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "vercel:index:Deployment")
		})

		t.Run("when applying, then the updated deployment serves HTML on the same alias URL", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			body := fetchAppText(t, appURL, bypassSecret)

			// Assert.
			require.Contains(t, body, "openbase vercel webapp fixture")
		})

		t.Run("when destroying, then the updated deployment is deleted cleanly", func(t *testing.T) {
			// Arrange / Act.
			deploymentID := h.Output(t, "dev", "deploymentId")
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v13/deployments/%s?teamId=%s", deploymentID, teamID), http.StatusNotFound)
		})
	})
}
