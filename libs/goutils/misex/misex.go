// Package misex provides Mise-aware subprocess execution helpers for OpenBase workspaces.
package misex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Public API -----------------------------------------------------------------

// CommandEnv builds a subprocess environment with GOWORK=off, MISE_TRUSTED_CONFIG_PATHS, and extraEnv.
func CommandEnv(repoRoot, workDir string, extraEnv []string) []string {
	trusted := workDir
	if repoRoot != "" && repoRoot != workDir {
		trusted = repoRoot + ":" + workDir
	}
	base := append(os.Environ(), "GOWORK=off", "MISE_TRUSTED_CONFIG_PATHS="+trusted)
	return append(base, extraEnv...)
}

// Run executes name with args inside workDir and returns stdout, attaching stderr to any error.
func Run(ctx context.Context, repoRoot, workDir string, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	cmd.Env = CommandEnv(repoRoot, workDir, extraEnv)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s\n%s", name, strings.Join(args, " "), err, string(out), stderr.String())
	}
	return string(out), nil
}

// RunCombined executes name with args inside workDir and returns combined stdout and stderr.
func RunCombined(ctx context.Context, repoRoot, workDir string, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	cmd.Env = CommandEnv(repoRoot, workDir, extraEnv)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}
