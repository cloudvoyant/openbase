package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cloudvoyant/openbase/libs/goutils/misex"
	"github.com/cloudvoyant/openbase/libs/testutils/fixtures"
)

// Private helpers ------------------------------------------------------------

func repositoryRoot(t *testing.T) string {
	t.Helper()
	return fixtures.RepoRoot(t)
}

func runCommand(t *testing.T, directory, name string, arguments ...string) string {
	t.Helper()
	return runCommandEnv(t, directory, nil, name, arguments...)
}

func runCommandEnv(t *testing.T, directory string, extraEnv []string, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	out, err := misex.Run(ctx, repositoryRoot(t), directory, extraEnv, name, arguments...)
	require.NoError(t, err)
	return out
}

func commandFailureEnv(t *testing.T, directory string, extraEnv []string, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := misex.RunCombined(ctx, repositoryRoot(t), directory, extraEnv, name, arguments...)
	require.Error(t, err, "%s unexpectedly succeeded:\n%s", name, out)
	return out
}

func copyFixture(t *testing.T, fixtureName string) string {
	t.Helper()
	return fixtures.Copy(t, fixtureName)
}

func copyFixtureTo(t *testing.T, fixtureName, destination string) string {
	t.Helper()
	return fixtures.CopyTo(t, fixtureName, destination)
}
