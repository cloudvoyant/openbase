// Package goutils provides general-purpose Go helpers shared across OpenBase packages and tests.
package goutils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Public API -----------------------------------------------------------------

// RunCommand executes name with args inside workDir using envOverrides merged over os.Environ() and returns stdout.
func RunCommand(ctx context.Context, workDir string, envOverrides map[string]string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	cmd.Env = mergeEnv(envOverrides)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s\n%s", name, strings.Join(args, " "), err, string(out), stderr.String())
	}
	return string(out), nil
}

// RunCommandCombined executes name with args inside workDir using envOverrides and returns combined stdout and stderr.
func RunCommandCombined(ctx context.Context, workDir string, envOverrides map[string]string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	cmd.Env = mergeEnv(envOverrides)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

// Private helpers ------------------------------------------------------------

func mergeEnv(overrides map[string]string) []string {
	env := os.Environ()
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}
