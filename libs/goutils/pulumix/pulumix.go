// Package pulumix provides reusable Pulumi CLI execution, secret redaction, and transcript logging helpers.
package pulumix

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudvoyant/openbase/libs/goutils/misex"
)

// Public API -----------------------------------------------------------------

// Redactor masks registered secret strings from CLI logs and error output.
type Redactor struct {
	mu      sync.Mutex
	secrets map[string]struct{}
}

// NewRedactor creates an empty thread-safe secret Redactor.
func NewRedactor() *Redactor {
	return &Redactor{secrets: map[string]struct{}{}}
}

// Add registers a non-trivial secret string (length >= 6) for redaction.
func (r *Redactor) Add(val string) {
	v := strings.TrimSpace(val)
	if len(v) < 6 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secrets[v] = struct{}{}
}

// AddEnvSlice scans KEY=VALUE entries and registers values whose keys look sensitive.
func (r *Redactor) AddEnvSlice(env []string) {
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || v == "" {
			continue
		}
		if isSensitiveKey(k) {
			r.Add(v)
		}
	}
}

// Redact replaces every registered secret in text with [REDACTED].
func (r *Redactor) Redact(text string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := text
	for sec := range r.secrets {
		out = strings.ReplaceAll(out, sec, "[REDACTED]")
	}
	return out
}

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

// DestroyWithRetry runs "pulumi destroy" with up to attempts retries to absorb transient cloud 5xx errors.
func DestroyWithRetry(
	ctx context.Context,
	repoRoot, workDir string,
	env []string,
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
		lastOut, lastErr = misex.RunCombined(
			ctx, repoRoot, workDir, env,
			"pulumi", "destroy", "--stack", stack, "--yes", "--non-interactive", "--skip-preview",
		)
		if lastErr == nil {
			return lastOut, nil
		}
	}
	return lastOut, lastErr
}

// AppendTranscript appends a redacted command invocation record to logPath.
func AppendTranscript(logPath, step string, args []string, exitCode int, out string, redactor *Redactor) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	body := out
	if redactor != nil {
		body = redactor.Redact(out)
	}
	_, err = fmt.Fprintf(f, "=== [%s] %s ===\n$ pulumi %s\nexit=%d\n%s\n\n",
		time.Now().UTC().Format(time.RFC3339), step, strings.Join(args, " "), exitCode, body)
	return err
}

// Private helpers ------------------------------------------------------------

func isSensitiveKey(key string) bool {
	u := strings.ToUpper(key)
	for _, needle := range []string{"SECRET", "TOKEN", "PASSWORD", "API_KEY", "DATABASE_URL", "DIRECT_URL"} {
		if strings.Contains(u, needle) {
			return true
		}
	}
	return false
}
