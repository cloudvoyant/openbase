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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := goutils.RunCommandCombined(ctx, directory, testutils.CommandEnvOverrides(repositoryRoot(t), directory, extraEnv), name, arguments...)
	require.Error(t, err, "%s unexpectedly succeeded:\n%s", name, out)
	return out
}

func copyFixture(t *testing.T, fixtureName string) string {
	t.Helper()
	return testutils.CopyFixture(t, fixtureName)
}

func copyFixtureTo(t *testing.T, fixtureName, destination string) string {
	t.Helper()
	return testutils.CopyFixtureTo(t, fixtureName, destination)
}
