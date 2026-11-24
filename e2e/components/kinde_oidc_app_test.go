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

	runResourceLifecycleMatrix(t, resourceLifecycleSpec{
		Fixture:       "kinde-oidc-app",
		PulumiProject: "openbase-kinde-oidc-app-fixture",
		Backend:       backend,
		HasShared:     true,
		Env: []string{
			"OPENBASE_TEST_APP_PROJECT=" + appProject,
			"OPENBASE_TEST_KINDE_APP_NAME=" + nonProdName,
			"OPENBASE_TEST_IMPORT_NONPROD_APP_ID=" + nonProdID,
			"OPENBASE_TEST_IMPORT_PROD_APP_ID=" + prodID,
			"OPENBASE_TEST_RETAIN_KINDE_APPS=1",
		},
		TargetStack: "dev",
		PlanToken:   "openbase:kinde:OidcApp",
		UpdateEnv:   []string{"OPENBASE_TEST_REVISION=v2"},
		AssertApplied: func(t *testing.T, h *LiveStackHarness, stack string) {
			require.Contains(t, h.Output(t, stack, "kindeRedirectUrl"), "/api/auth/kinde_callback")
		},
		AssertUpdated: func(t *testing.T, h *LiveStackHarness, stack string) {
			require.Contains(t, h.Output(t, stack, "kindeRedirectUrl"), "/api/auth/callback")
		},
	})
}
