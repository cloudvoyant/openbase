// Package liveenv provides sandbox credential checks for live cloud E2E test suites.
package liveenv

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cloudvoyant/openbase/libs/goutils/misex"
)

// Public API -----------------------------------------------------------------

// RequireGCP verifies live GCP sandbox flags and returns (gcpProject, backendOverride).
func RequireGCP(t *testing.T) (string, string) {
	t.Helper()
	if !IsLiveEnabled("OPENBASE_TEST_GCP") {
		t.Skip("set OPENBASE_TEST_GCP=1 or OPENBASE_TEST_LIVE=1 to run against live GCP")
	}
	project := FirstNonEmpty(os.Getenv("OPENBASE_TEST_GCP_PROJECT"), os.Getenv("PREMISE_TEST_GCP_PROJECT"))
	require.NotEmpty(t, project, "set OPENBASE_TEST_GCP_PROJECT for live GCP E2E")
	return project, OptionalBackend()
}

// RequireNeon verifies live Neon sandbox flags and NEON_API_KEY, returning backendOverride.
func RequireNeon(t *testing.T) string {
	t.Helper()
	if !IsLiveEnabled("OPENBASE_TEST_NEON") {
		t.Skip("set OPENBASE_TEST_NEON=1 or OPENBASE_TEST_LIVE=1 to run against live Neon")
	}
	RequireVars(t, "NEON_API_KEY")
	return OptionalBackend()
}

// RequireKinde verifies live Kinde NonProd and Prod M2M env vars, returning backendOverride.
func RequireKinde(t *testing.T) string {
	t.Helper()
	if !IsLiveEnabled("OPENBASE_TEST_KINDE") {
		t.Skip("set OPENBASE_TEST_KINDE=1 or OPENBASE_TEST_LIVE=1 to run against live Kinde")
	}
	backfillNonProdKindeEnv(t)
	RequireVars(
		t,
		"KINDE_NONPROD_TENANT_DOMAIN", "KINDE_NONPROD_M2M_CLIENT_ID", "KINDE_NONPROD_M2M_CLIENT_SECRET",
		"KINDE_PROD_TENANT_DOMAIN", "KINDE_PROD_M2M_CLIENT_ID", "KINDE_PROD_M2M_CLIENT_SECRET",
	)
	return OptionalBackend()
}

// RequireVercel verifies live Vercel sandbox flags and VERCEL_TOKEN, returning backendOverride.
func RequireVercel(t *testing.T) string {
	t.Helper()
	if !IsLiveEnabled("OPENBASE_TEST_VERCEL") {
		t.Skip("set OPENBASE_TEST_VERCEL=1 or OPENBASE_TEST_LIVE=1 to run against live Vercel")
	}
	RequireVars(t, "VERCEL_TOKEN")
	return OptionalBackend()
}

// RequireKindeUser verifies AgentMail OTP or Kinde email/password credentials for browser login tests.
func RequireKindeUser(t *testing.T) (string, string) {
	t.Helper()
	email := FirstNonEmpty(os.Getenv("OPENBASE_E2E_EMAIL"), os.Getenv("E2E_P1_EMAIL"), os.Getenv("KINDE_E2E_USERNAME"))
	require.NotEmpty(t, email, "missing E2E login email: set E2E_P1_EMAIL (with AGENTMAIL_API_KEY) or KINDE_E2E_USERNAME")
	require.True(t, os.Getenv("AGENTMAIL_API_KEY") != "" || os.Getenv("KINDE_E2E_PASSWORD") != "",
		"missing E2E login credential: set AGENTMAIL_API_KEY or KINDE_E2E_PASSWORD")
	return email, os.Getenv("KINDE_E2E_PASSWORD")
}

// ResolveGCPProjectNumber returns the numeric GCP project number via env override or gcloud CLI.
func ResolveGCPProjectNumber(t *testing.T, repoRoot, gcpProject string) string {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv("OPENBASE_TEST_GCP_PROJECT_NUMBER")); v != "" {
		return v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := misex.Run(ctx, repoRoot, repoRoot, nil, "gcloud", "projects", "describe", gcpProject, "--format=value(projectNumber)")
	require.NoError(t, err)
	num := strings.TrimSpace(out)
	require.NotEmpty(t, num, "failed to resolve GCP project number for %s", gcpProject)
	return num
}

// IsLiveEnabled reports whether key or OPENBASE_TEST_LIVE is set to "1".
func IsLiveEnabled(key string) bool {
	return os.Getenv("OPENBASE_TEST_LIVE") == "1" || os.Getenv(key) == "1"
}

// OptionalBackend returns the optional Pulumi backend override URL from the environment.
func OptionalBackend() string {
	return FirstNonEmpty(os.Getenv("OPENBASE_TEST_PULUMI_BACKEND_URL"), os.Getenv("PULUMI_BACKEND_URL"))
}

// RequireVars fails t when any listed environment variable is empty.
func RequireVars(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		require.NotEmpty(t, os.Getenv(k), "missing required environment variable %s", k)
	}
}

// FirstNonEmpty returns the first trimmed non-empty string in vals.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Private helpers ------------------------------------------------------------

func backfillNonProdKindeEnv(t *testing.T) {
	t.Helper()
	pairs := [][2]string{
		{"KINDE_NONPROD_TENANT_DOMAIN", "KINDE_DEV_TENANT_DOMAIN"},
		{"KINDE_NONPROD_M2M_CLIENT_ID", "KINDE_DEV_M2M_CLIENT_ID"},
		{"KINDE_NONPROD_M2M_CLIENT_SECRET", "KINDE_DEV_M2M_CLIENT_SECRET"},
	}
	for _, p := range pairs {
		if os.Getenv(p[0]) == "" && os.Getenv(p[1]) != "" {
			t.Setenv(p[0], os.Getenv(p[1]))
		}
	}
}
