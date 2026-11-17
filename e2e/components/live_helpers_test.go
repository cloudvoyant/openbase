package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Public API -----------------------------------------------------------------

// LiveStackHarness manages an isolated temporary Pulumi consumer checkout, artifact capture, and stack cleanup.
type LiveStackHarness struct {
	Dir        string
	Project    string
	BackendURL string
	PulumiHome string
	OutputDir  string
	extraEnv   []string
	order      []string
	touched    map[string]bool
	verifiers  []func(t *testing.T)
	mu         sync.Mutex
	secrets    []string
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

// NewLiveHarness copies fixtureName into t.TempDir(), prepares e2e/outputs/components/<Test>, logs in, and registers t.Cleanup.
func NewLiveHarness(t *testing.T, fixtureName, pulumiProject, baseBackend string, extraEnv ...string) *LiveStackHarness {
	t.Helper()
	dir := copyFixture(t, fixtureName)
	pulumiHome := t.TempDir()
	runBackend := prepareBackendPath(t, baseBackend)
	outDir := prepareArtifactDir(t)
	h := &LiveStackHarness{
		Dir:        dir,
		Project:    pulumiProject,
		BackendURL: runBackend,
		PulumiHome: pulumiHome,
		OutputDir:  outDir,
		extraEnv:   extraEnv,
		touched:    map[string]bool{},
	}
	h.seedAmbientSecrets(extraEnv)
	h.Pulumi(t, nil, "login", runBackend)
	t.Cleanup(func() { h.cleanup(t) })
	return h
}

// RegisterSecret registers a sensitive value so command transcripts and saved artifacts redact it.
func (h *LiveStackHarness) RegisterSecret(values ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, v := range values {
		h.addSecretLocked(v)
	}
}

// WriteArtifact writes secret-redacted text to e2e/outputs/components/<Test>/<filename> for human inspection.
func (h *LiveStackHarness) WriteArtifact(t *testing.T, filename, content string) string {
	t.Helper()
	target := filepath.Join(h.OutputDir, filename)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	if err := os.WriteFile(target, []byte(h.redact(content)), 0o644); err != nil {
		t.Fatalf("write artifact %s: %v", filename, err)
	}
	return target
}

// WriteJSONArtifact marshals v as indented JSON and writes a secret-redacted snapshot under e2e/outputs/components/<Test>.
func (h *LiveStackHarness) WriteJSONArtifact(t *testing.T, filename string, v any) string {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal artifact %s: %v", filename, err)
	}
	return h.WriteArtifact(t, filename, string(raw)+"\n")
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

// Pulumi executes a pulumi CLI command expected to succeed and appends to commands.log.
func (h *LiveStackHarness) Pulumi(t *testing.T, callEnv []string, args ...string) string {
	t.Helper()
	h.seedEnvSliceSecrets(callEnv)
	out := runCommandEnv(t, h.Dir, h.env(callEnv), "pulumi", args...)
	if hasArg(args, "--show-secrets") {
		h.maybeRegisterSecretOutput(args, strings.TrimSpace(out))
	}
	h.appendTranscript(t.Name(), args, 0, out)
	return out
}

// PulumiFail executes a pulumi CLI command expected to fail, records commands.log, and returns combined output.
func (h *LiveStackHarness) PulumiFail(t *testing.T, callEnv []string, args ...string) string {
	t.Helper()
	h.seedEnvSliceSecrets(callEnv)
	out := commandFailureEnv(t, h.Dir, h.env(callEnv), "pulumi", args...)
	h.appendTranscript(t.Name(), args, 1, out)
	return out
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

var ambientSecretEnvKeys = []string{
	"VERCEL_TOKEN",
	"NEON_API_KEY",
	"KINDE_DEV_M2M_CLIENT_SECRET",
	"KINDE_PROD_M2M_CLIENT_SECRET",
	"OPENBASE_TEST_SECRET_VALUE",
}

func prepareBackendPath(t *testing.T, baseBackend string) string {
	t.Helper()
	runBackend := fmt.Sprintf("%s/run-%d", strings.TrimRight(baseBackend, "/"), time.Now().UnixNano())
	if strings.HasPrefix(runBackend, "file://") {
		if err := os.MkdirAll(strings.TrimPrefix(runBackend, "file://"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return runBackend
}

func prepareArtifactDir(t *testing.T) string {
	t.Helper()
	suiteTest, _, _ := strings.Cut(t.Name(), "/")
	outDir := filepath.Join(repositoryRoot(t), "e2e", "outputs", "components", suiteTest)
	if err := os.RemoveAll(outDir); err != nil {
		t.Fatalf("clean artifact dir: %v", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	return outDir
}

func (h *LiveStackHarness) seedAmbientSecrets(extraEnv []string) {
	for _, key := range ambientSecretEnvKeys {
		if v := os.Getenv(key); v != "" {
			h.RegisterSecret(v)
		}
	}
	h.seedEnvSliceSecrets(extraEnv)
}

func (h *LiveStackHarness) seedEnvSliceSecrets(entries []string) {
	for _, kv := range entries {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || v == "" {
			continue
		}
		upper := strings.ToUpper(k)
		if strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "PASSWORD") {
			h.RegisterSecret(v)
		}
	}
}

func (h *LiveStackHarness) maybeRegisterSecretOutput(args []string, val string) {
	if val == "" || len(args) < 3 {
		return
	}
	lowerKey := strings.ToLower(args[2])
	if strings.Contains(lowerKey, "secret") || strings.Contains(lowerKey, "token") || strings.HasPrefix(val, "postgres") {
		h.RegisterSecret(val)
	}
}

func (h *LiveStackHarness) addSecretLocked(raw string) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) < 6 {
		return
	}
	h.secrets = append(h.secrets, trimmed)
	if parsed, err := url.Parse(trimmed); err == nil && parsed.User != nil {
		if pw, ok := parsed.User.Password(); ok && len(pw) >= 6 {
			h.secrets = append(h.secrets, pw)
		}
	}
}

func (h *LiveStackHarness) redact(input string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := input
	for _, s := range h.secrets {
		out = strings.ReplaceAll(out, s, "[REDACTED]")
	}
	return out
}

func (h *LiveStackHarness) appendTranscript(testName string, args []string, exitCode int, output string) {
	entry := fmt.Sprintf(
		"=== [%s] $ pulumi %s (exit %d) ===\n%s\n",
		testName,
		h.redact(strings.Join(args, " ")),
		exitCode,
		strings.TrimSpace(h.redact(output)),
	)
	logPath := filepath.Join(h.OutputDir, "commands.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(entry)
}

func hasArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

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
		t.Errorf("cleanup destroy stack %s: %v\n%s", stack, err, h.redact(string(out)))
		return
	}
	rm := exec.CommandContext(ctx, "pulumi", "stack", "rm", stack, "--yes", "--non-interactive")
	rm.Dir = h.Dir
	rm.Env = baseEnv
	_ = rm.Run()
}
