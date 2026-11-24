// Package testutils provides test-only fixture materialization and sandbox environment checks for OpenBase suites.
package testutils

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cloudvoyant/openbase/libs/goutils"
)

// Public API -----------------------------------------------------------------

// RepoRoot returns the absolute path to the OpenBase repository root.
func RepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "unable to resolve caller path")
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// CommandEnvOverrides returns the standard Mise/Go test env overrides merged with extra KEY=VALUE pairs.
func CommandEnvOverrides(repoRoot, workDir string, extraEnv []string) map[string]string {
	trusted := workDir
	if repoRoot != "" && repoRoot != workDir {
		trusted = repoRoot + ":" + workDir
	}
	out := map[string]string{
		"GOWORK":                    "off",
		"MISE_TRUSTED_CONFIG_PATHS": trusted,
	}
	for _, kv := range extraEnv {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// CopyFixture copies e2e/components/testdata/<fixtureName> into a temporary test directory and runs go mod tidy.
func CopyFixture(t *testing.T, fixtureName string) string {
	t.Helper()
	return CopyFixtureTo(t, fixtureName, filepath.Join(t.TempDir(), fixtureName))
}

// CopyFixtureTo copies e2e/components/testdata/<fixtureName> into destination, substitutes {{REPO_ROOT}}, and runs go mod tidy.
func CopyFixtureTo(t *testing.T, fixtureName, destination string) string {
	t.Helper()
	root := RepoRoot(t)
	source := filepath.Join(root, "e2e", "components", "testdata", fixtureName)
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if shouldSkipFixtureEntry(entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return copyFixtureEntry(source, destination, root, path, entry)
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, err = goutils.RunCommand(ctx, destination, CommandEnvOverrides(root, destination, nil), "go", "mod", "tidy")
	require.NoError(t, err)
	return destination
}

// Private helpers ------------------------------------------------------------

func shouldSkipFixtureEntry(entry fs.DirEntry) bool {
	name := entry.Name()
	if name == ".pulumi" || name == ".pulumi-state" || name == "node_modules" || name == "__pycache__" {
		return true
	}
	return strings.HasPrefix(name, "Pulumi.") && name != "Pulumi.yaml"
}

func copyFixtureEntry(source, destination, repoRoot, path string, entry fs.DirEntry) error {
	relative, err := filepath.Rel(source, path)
	if err != nil {
		return err
	}
	if relative == "go.mod.tmpl" {
		relative = "go.mod"
	}
	target := filepath.Join(destination, relative)
	if entry.IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if !entry.Type().IsRegular() {
		return fmt.Errorf("fixture contains non-regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	rendered := strings.ReplaceAll(string(data), "{{REPO_ROOT}}", filepath.ToSlash(repoRoot))
	info, err := entry.Info()
	if err != nil {
		return err
	}
	return os.WriteFile(target, []byte(rendered), info.Mode().Perm())
}
