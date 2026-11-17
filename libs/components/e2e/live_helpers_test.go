package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Public API -----------------------------------------------------------------

// LiveStackHarness manages an isolated temporary Pulumi consumer checkout and stack cleanup.
type LiveStackHarness struct {
	Dir        string
	Project    string
	BackendURL string
	PulumiHome string
	extraEnv   []string
	order      []string
	touched    map[string]bool
	verifiers  []func(t *testing.T)
}

// RequireLiveGCP checks OPENBASE_TEST_GCP/OPENBASE_TEST_LIVE and returns (gcpProject, gsBackend).
func RequireLiveGCP(t *testing.T) (string, string) {
	t.Helper()
	if !isLiveEnabled("OPENBASE_TEST_GCP") {
		t.Skip("set OPENBASE_TEST_GCP=1 or OPENBASE_TEST_LIVE=1 to run against live GCP")
	}
	backend := requireRemoteBackend(t)
	project := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
	if project == "" {
		project = os.Getenv("PREMISE_TEST_GCP_PROJECT")
	}
	if project == "" {
		t.Fatal("set OPENBASE_TEST_GCP_PROJECT (or PREMISE_TEST_GCP_PROJECT) for live GCP E2E")
	}
	return project, backend
}

// RequireLiveNeon checks OPENBASE_TEST_NEON/OPENBASE_TEST_LIVE and returns gsBackend.
func RequireLiveNeon(t *testing.T) string {
	t.Helper()
	if !isLiveEnabled("OPENBASE_TEST_NEON") {
		t.Skip("set OPENBASE_TEST_NEON=1 or OPENBASE_TEST_LIVE=1 to run against live Neon")
	}
	backend := requireRemoteBackend(t)
	requireEnvVars(t, "NEON_API_KEY")
	return backend
}

// RequireLiveKinde checks OPENBASE_TEST_KINDE/OPENBASE_TEST_LIVE and returns gsBackend.
func RequireLiveKinde(t *testing.T) string {
	t.Helper()
	if !isLiveEnabled("OPENBASE_TEST_KINDE") {
		t.Skip("set OPENBASE_TEST_KINDE=1 or OPENBASE_TEST_LIVE=1 to run against live Kinde")
	}
	backend := requireRemoteBackend(t)
	requireEnvVars(
		t,
		"KINDE_DEV_TENANT_DOMAIN", "KINDE_DEV_M2M_CLIENT_ID", "KINDE_DEV_M2M_CLIENT_SECRET",
		"KINDE_PROD_TENANT_DOMAIN", "KINDE_PROD_M2M_CLIENT_ID", "KINDE_PROD_M2M_CLIENT_SECRET",
	)
	return backend
}

// RequireLiveVercel checks OPENBASE_TEST_VERCEL/OPENBASE_TEST_LIVE and returns gsBackend.
func RequireLiveVercel(t *testing.T) string {
	t.Helper()
	if !isLiveEnabled("OPENBASE_TEST_VERCEL") {
		t.Skip("set OPENBASE_TEST_VERCEL=1 or OPENBASE_TEST_LIVE=1 to run against live Vercel")
	}
	backend := requireRemoteBackend(t)
	requireEnvVars(t, "VERCEL_TOKEN")
	return backend
}

// NewLiveHarness copies fixtureName into t.TempDir(), logs into baseBackend/run-<nano>, and registers t.Cleanup.
func NewLiveHarness(t *testing.T, fixtureName, pulumiProject, baseBackend string, extraEnv ...string) *LiveStackHarness {
	t.Helper()
	dir := copyFixture(t, fixtureName)
	pulumiHome := t.TempDir()
	runBackend := fmt.Sprintf("%s/run-%d", strings.TrimRight(baseBackend, "/"), time.Now().UnixNano())
	if strings.HasPrefix(runBackend, "file://") {
		if err := os.MkdirAll(strings.TrimPrefix(runBackend, "file://"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h := &LiveStackHarness{
		Dir:        dir,
		Project:    pulumiProject,
		BackendURL: runBackend,
		PulumiHome: pulumiHome,
		extraEnv:   extraEnv,
		touched:    map[string]bool{},
	}
	h.Pulumi(t, nil, "login", runBackend)
	t.Cleanup(func() { h.cleanup(t) })
	return h
}

// InitStack initializes stack and records it for reverse-order cleanup.
func (h *LiveStackHarness) InitStack(t *testing.T, stack string) {
	t.Helper()
	if !h.touched[stack] {
		h.touched[stack] = true
		h.order = append(h.order, stack)
	}
	h.Pulumi(t, nil, "stack", "init", stack, "--non-interactive")
}

// Pulumi executes a pulumi CLI command expected to succeed.
func (h *LiveStackHarness) Pulumi(t *testing.T, callEnv []string, args ...string) string {
	t.Helper()
	return runCommandEnv(t, h.Dir, h.env(callEnv), "pulumi", args...)
}

// PulumiFail executes a pulumi CLI command expected to fail and returns its combined output.
func (h *LiveStackHarness) PulumiFail(t *testing.T, callEnv []string, args ...string) string {
	t.Helper()
	return commandFailureEnv(t, h.Dir, h.env(callEnv), "pulumi", args...)
}

// Output returns a trimmed stack output (decrypting secrets when present).
func (h *LiveStackHarness) Output(t *testing.T, stack, key string) string {
	t.Helper()
	out := h.Pulumi(t, nil, "stack", "output", key, "--stack", stack, "--show-secrets")
	return strings.TrimSpace(out)
}

// OnCleanupVerify appends a post-destroy verification hook run during t.Cleanup.
func (h *LiveStackHarness) OnCleanupVerify(fn func(t *testing.T)) {
	h.verifiers = append(h.verifiers, fn)
}

// Private helpers ------------------------------------------------------------

func isLiveEnabled(flag string) bool {
	return os.Getenv("OPENBASE_TEST_LIVE") == "1" || os.Getenv(flag) == "1"
}

func requireRemoteBackend(t *testing.T) string {
	t.Helper()
	backend := strings.TrimRight(os.Getenv("PULUMI_BACKEND_URL"), "/")
	if !strings.HasPrefix(backend, "gs://") {
		t.Fatal("set PULUMI_BACKEND_URL to an existing gs:// state bucket prefix")
	}
	return backend
}

func requireEnvVars(t *testing.T, keys ...string) {
	t.Helper()
	var missing []string
	for _, k := range keys {
		if os.Getenv(k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("missing required live E2E environment variables: %s", strings.Join(missing, ", "))
	}
}

func (h *LiveStackHarness) env(callEnv []string) []string {
	base := []string{
		"PULUMI_HOME=" + h.PulumiHome,
		"PULUMI_BACKEND_URL=" + h.BackendURL,
		"PULUMI_CONFIG_PASSPHRASE=",
		"PULUMI_SKIP_UPDATE_CHECK=true",
	}
	base = append(base, h.extraEnv...)
	return append(base, callEnv...)
}

func (h *LiveStackHarness) cleanup(t *testing.T) {
	t.Helper()
	for i := len(h.order) - 1; i >= 0; i-- {
		h.destroyStackForCleanup(t, h.order[i])
	}
	for _, verify := range h.verifiers {
		verify(t)
	}
}

func (h *LiveStackHarness) destroyStackForCleanup(t *testing.T, stack string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	baseEnv := append(os.Environ(), h.env(nil)...)
	unprotect := exec.CommandContext(ctx, "pulumi", "state", "unprotect", "--all", "--yes", "--stack", stack)
	unprotect.Dir = h.Dir
	unprotect.Env = baseEnv
	_ = unprotect.Run()

	destroy := exec.CommandContext(ctx, "pulumi", "destroy", "--stack", stack, "--yes", "--non-interactive", "--skip-preview")
	destroy.Dir = h.Dir
	destroy.Env = baseEnv
	if out, err := destroy.CombinedOutput(); err != nil {
		t.Errorf("cleanup destroy stack %s: %v\n%s", stack, err, out)
		return
	}
	rm := exec.CommandContext(ctx, "pulumi", "stack", "rm", stack, "--yes", "--non-interactive")
	rm.Dir = h.Dir
	rm.Env = baseEnv
	_ = rm.Run()
}
