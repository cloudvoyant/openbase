package e2e_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Private helpers ------------------------------------------------------------

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve e2e caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func runCommand(t *testing.T, directory, name string, arguments ...string) string {
	t.Helper()
	return runCommandEnv(t, directory, nil, name, arguments...)
}

func runCommandEnv(t *testing.T, directory string, extraEnv []string, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = append(append(os.Environ(), "GOWORK=off", "MISE_TRUSTED_CONFIG_PATHS="+repositoryRoot(t)+":"+directory), extraEnv...)
	var diagnostics strings.Builder
	command.Stderr = &diagnostics
	output, err := command.Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s\n%s", name, strings.Join(arguments, " "), err, output, diagnostics.String())
	}
	return string(output)
}

func commandFailureEnv(t *testing.T, directory string, extraEnv []string, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = append(append(os.Environ(), "GOWORK=off", "MISE_TRUSTED_CONFIG_PATHS="+repositoryRoot(t)+":"+directory), extraEnv...)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("%s %s unexpectedly succeeded:\n%s", name, strings.Join(arguments, " "), output)
	}
	return string(output)
}

func copyFixture(t *testing.T, fixtureName string) string {
	t.Helper()
	repoRoot := repositoryRoot(t)
	source := filepath.Join(repoRoot, "libs", "components", "e2e", "testdata", fixtureName)
	destination := filepath.Join(t.TempDir(), fixtureName)
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
		return copyFixtureEntry(source, destination, repoRoot, path, entry)
	})
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, destination, "go", "mod", "tidy")
	return destination
}

func shouldSkipFixtureEntry(entry fs.DirEntry) bool {
	name := entry.Name()
	if name == ".pulumi" || name == "node_modules" {
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
