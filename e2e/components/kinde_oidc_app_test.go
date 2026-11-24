package e2e_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Public API -----------------------------------------------------------------

func TestKindeOidcApp(t *testing.T) {
	backend := RequireLiveKinde(t)
	appProject := fmt.Sprintf("obkd%d", time.Now().Unix()%100000)
	nonProdID, nonProdName := prepareKindeSandboxByPrefix(t, "non-prod", "openbase-e2e-kd-", "openbase-e2e-kd-"+appProject,
		[]string{"http://localhost:5173/api/auth/kinde_callback"}, []string{"http://localhost:5173/"})
	prodID, _ := prepareKindeSandboxByPrefix(t, "prod", "openbase-e2e-kd-", "openbase-e2e-kd-"+appProject,
		[]string{"https://example.com/api/auth/kinde_callback"}, []string{"https://example.com/"})

	h := NewLiveHarness(
		t,
		"kinde-oidc-app",
		"openbase-kinde-oidc-app-fixture",
		backend,
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_KINDE_APP_NAME="+nonProdName,
		"OPENBASE_TEST_IMPORT_NONPROD_APP_ID="+nonProdID,
		"OPENBASE_TEST_IMPORT_PROD_APP_ID="+prodID,
		"OPENBASE_TEST_RETAIN_KINDE_APPS=1",
	)
	h.InitStack(t, "shared")
	h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
	nonProdClientID := h.Output(t, "shared", "nonProdClientId")
	h.InitStack(t, "dev")

	t.Run("given no infra exists", func(t *testing.T) {
		t.Run("when planning, then preview plans the Kinde OidcApp component without mutating Kinde apps", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "dev")
			h.WriteArtifact(t, "fresh-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "openbase:kinde:OidcApp")
		})

		t.Run("when applying", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			issuer := h.Output(t, "dev", "kindeIssuerUrl")
			redirectURL := h.Output(t, "dev", "kindeRedirectUrl")
			okStatus, okLoc, _ := callKindeAuthorize(t, issuer, nonProdClientID, redirectURL)
			badStatus, badLoc, badBody := callKindeAuthorize(t, issuer, nonProdClientID, "https://evil.example.com/callback")

			// Assert.
			t.Run("then Kinde accepts the registered dev redirect URI and rejects unregistered redirects", func(t *testing.T) {
				require.Contains(t, redirectURL, "/api/auth/kinde_callback")
				assertLoginRedirect(t, redirectURL, okStatus, okLoc)
				assertRedirectRejected(t, "https://evil.example.com/callback", badStatus, badLoc, badBody)
			})
		})

		t.Run("when destroying, then the env stack tears down while keeping the shared Kinde app intact", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			require.GreaterOrEqual(t, countKindeAppsByPrefix(t, "non-prod", "openbase-e2e-kd-"), 1)
		})
	})

	t.Run("given prior infra exists", func(t *testing.T) {
		// Arrange (converged state).
		h.Pulumi(t, nil, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

		t.Run("when planning, then preview plans the updated callback path cleanly", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "preview", "--stack", "dev")
			h.WriteArtifact(t, "update-plan.txt", planOut)

			// Assert.
			require.Contains(t, planOut, "openbase:kinde:OidcApp")
		})

		t.Run("when applying, then kindeRedirectUrl updates in place to the new callback path", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=v2"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			require.Contains(t, h.Output(t, "dev", "kindeRedirectUrl"), "/api/auth/callback")
		})

		t.Run("when destroying, then the env stack tears down cleanly", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "destroy", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			require.GreaterOrEqual(t, countKindeAppsByPrefix(t, "non-prod", "openbase-e2e-kd-"), 1)
		})
	})
}
