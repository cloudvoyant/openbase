package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cloudvoyant/openbase/libs/goutils"
	"github.com/cloudvoyant/openbase/libs/testutils"
)

// Private helpers ------------------------------------------------------------

type LiveStackHarness = testutils.LiveStackHarness

type browserAuthReport = testutils.BrowserAuthReport

func NewLiveHarness(t *testing.T, fixtureName, pulumiProject, baseBackend string, extraEnv ...string) *LiveStackHarness {
	t.Helper()
	return testutils.NewLiveHarness(t, fixtureName, pulumiProject, baseBackend, extraEnv...)
}

func RequireLiveGCP(t *testing.T) (string, string) {
	t.Helper()
	return testutils.RequireGCP(t)
}

func RequireLiveNeon(t *testing.T) string {
	t.Helper()
	return testutils.RequireNeon(t)
}

func RequireLiveKinde(t *testing.T) string {
	t.Helper()
	return testutils.RequireKinde(t)
}

func RequireLiveVercel(t *testing.T) string {
	t.Helper()
	return testutils.RequireVercel(t)
}

func RequireLiveKindeUser(t *testing.T) (string, string) {
	t.Helper()
	return testutils.RequireKindeUser(t)
}

func ResolveGCPProjectNumber(t *testing.T, gcpProject string) string {
	t.Helper()
	return testutils.ResolveGCPProjectNumber(t, repositoryRoot(t), gcpProject)
}

func verifyKindeBrowserLogin(t *testing.T, h *LiveStackHarness, baseURL, bypassSecret string) testutils.BrowserAuthReport {
	t.Helper()
	return testutils.VerifyKindeBrowserLogin(t, h, baseURL, bypassSecret)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	return testutils.RepoRoot(t)
}

func runCommand(t *testing.T, directory, name string, arguments ...string) string {
	t.Helper()
	return runCommandEnv(t, directory, nil, name, arguments...)
}

func runCommandEnv(t *testing.T, directory string, extraEnv []string, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	out, err := goutils.RunCommand(ctx, directory, testutils.CommandEnvOverrides(repositoryRoot(t), directory, extraEnv), name, arguments...)
	require.NoError(t, err)
	return out
}

func commandFailureEnv(t *testing.T, directory string, extraEnv []string, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := goutils.RunCommandCombined(ctx, directory, testutils.CommandEnvOverrides(repositoryRoot(t), directory, extraEnv), name, arguments...)
	require.Error(t, err, "expected %s %v to fail, got:\n%s", name, arguments, out)
	return out
}

func copyFixture(t *testing.T, fixtureName string) string {
	t.Helper()
	return testutils.CopyFixture(t, fixtureName)
}
