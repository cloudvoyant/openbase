package goutils

import (
	"context"
	"strings"
	"time"
)

// Public API -----------------------------------------------------------------

// StackFromArgs extracts the stack name from "--stack <name>" or "-s <name>" CLI flags.
func StackFromArgs(args []string) string {
	for i, a := range args {
		if (a == "--stack" || a == "-s") && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, "--stack=") {
			return strings.TrimPrefix(a, "--stack=")
		}
	}
	return ""
}

// DestroyPulumiStackWithRetry runs "pulumi destroy" with up to attempts retries to absorb transient cloud 5xx errors.
func DestroyPulumiStackWithRetry(
	ctx context.Context,
	workDir string,
	envOverrides map[string]string,
	stack string,
	attempts int,
) (string, error) {
	if attempts < 1 {
		attempts = 1
	}
	var lastOut string
	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}
		lastOut, lastErr = RunCommandCombined(
			ctx, workDir, envOverrides,
			"pulumi", "destroy", "--stack", stack, "--yes", "--non-interactive", "--skip-preview",
		)
		if lastErr == nil {
			return lastOut, nil
		}
	}
	return lastOut, lastErr
}
