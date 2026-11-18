package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Public API -----------------------------------------------------------------

// RunManifest records non-secret stack and workspace metadata under e2e/outputs/components/<Test>/run-manifest.json.
type RunManifest struct {
	TestName        string            `json:"testName"`
	Fixture         string            `json:"fixture"`
	Project         string            `json:"project"`
	WorkspaceDir    string            `json:"workspaceDir"`
	PulumiHome      string            `json:"pulumiHome"`
	BackendURL      string            `json:"backendUrl"`
	Stacks          []string          `json:"stacks"`
	Env             map[string]string `json:"env,omitempty"`
	LiveURLs        map[string]string `json:"liveUrls,omitempty"`
	VercelProjectID string            `json:"vercelProjectId,omitempty"`
	VercelTeamID    string            `json:"vercelTeamId,omitempty"`
	BlobBaseURL     string            `json:"blobBaseUrl,omitempty"`
	CleanedUp       bool              `json:"cleanedUp"`
	UpdatedAt       string            `json:"updatedAt"`
}

// LiveStackHarness manages an isolated Pulumi consumer checkout, output-dir state, artifact capture, and cleanup.
type LiveStackHarness struct {
	TestName        string
	Fixture         string
	Dir             string
	Project         string
	BackendURL      string
	PulumiHome      string
	OutputDir       string
	Keep            bool
	extraEnv        []string
	order           []string
	touched         map[string]bool
	liveURLs        map[string]string
	vercelProjectID string
	vercelTeamID    string
	blobBaseURL     string
	verifiers       []func(t *testing.T)
	mu              sync.Mutex
	secrets         []string
}

// RequireLiveGCP checks OPENBASE_TEST_GCP/OPENBASE_TEST_LIVE and returns (gcpProject, backendOverride).
func RequireLiveGCP(t *testing.T) (string, string) {
	t.Helper()
	if !isLiveEnabled("OPENBASE_TEST_GCP") {
		t.Skip("set OPENBASE_TEST_GCP=1 or OPENBASE_TEST_LIVE=1 to run against live GCP")
	}
	project := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
	if project == "" {
		project = os.Getenv("PREMISE_TEST_GCP_PROJECT")
	}
	if project == "" {
		t.Fatal("set OPENBASE_TEST_GCP_PROJECT (or PREMISE_TEST_GCP_PROJECT) for live GCP E2E")
	}
	return project, optionalBackendEnv()
}

// RequireLiveNeon checks OPENBASE_TEST_NEON/OPENBASE_TEST_LIVE and returns backendOverride.
func RequireLiveNeon(t *testing.T) string {
	t.Helper()
	if !isLiveEnabled("OPENBASE_TEST_NEON") {
		t.Skip("set OPENBASE_TEST_NEON=1 or OPENBASE_TEST_LIVE=1 to run against live Neon")
	}
	requireEnvVars(t, "NEON_API_KEY")
	return optionalBackendEnv()
}

// RequireLiveKinde checks OPENBASE_TEST_KINDE/OPENBASE_TEST_LIVE and returns backendOverride.
func RequireLiveKinde(t *testing.T) string {
	t.Helper()
	if !isLiveEnabled("OPENBASE_TEST_KINDE") {
		t.Skip("set OPENBASE_TEST_KINDE=1 or OPENBASE_TEST_LIVE=1 to run against live Kinde")
	}
	requireEnvVars(
		t,
		"KINDE_DEV_TENANT_DOMAIN", "KINDE_DEV_M2M_CLIENT_ID", "KINDE_DEV_M2M_CLIENT_SECRET",
		"KINDE_PROD_TENANT_DOMAIN", "KINDE_PROD_M2M_CLIENT_ID", "KINDE_PROD_M2M_CLIENT_SECRET",
	)
	return optionalBackendEnv()
}

// RequireLiveVercel checks OPENBASE_TEST_VERCEL/OPENBASE_TEST_LIVE and returns backendOverride.
func RequireLiveVercel(t *testing.T) string {
	t.Helper()
	if !isLiveEnabled("OPENBASE_TEST_VERCEL") {
		t.Skip("set OPENBASE_TEST_VERCEL=1 or OPENBASE_TEST_LIVE=1 to run against live Vercel")
	}
	requireEnvVars(t, "VERCEL_TOKEN")
	return optionalBackendEnv()
}

// NewLiveHarness materializes fixtureName + Pulumi state under e2e/outputs/components/<Test>, logs in, and registers cleanup.
func NewLiveHarness(t *testing.T, fixtureName, pulumiProject, baseBackend string, extraEnv ...string) *LiveStackHarness {
	t.Helper()
	suiteTest, _, _ := strings.Cut(t.Name(), "/")
	outDir := prepareArtifactDir(t, suiteTest)
	dir := copyFixtureTo(t, fixtureName, filepath.Join(outDir, "workspace"))
	pulumiHome := filepath.Join(outDir, "pulumi-home")
	if err := os.MkdirAll(pulumiHome, 0o755); err != nil {
		t.Fatal(err)
	}
	runBackend := prepareBackendPath(t, outDir, baseBackend)
	h := &LiveStackHarness{
		TestName:   suiteTest,
		Fixture:    fixtureName,
		Dir:        dir,
		Project:    pulumiProject,
		BackendURL: runBackend,
		PulumiHome: pulumiHome,
		OutputDir:  outDir,
		Keep:       isKeepEnabled(),
		extraEnv:   extraEnv,
		touched:    map[string]bool{},
		liveURLs:   map[string]string{},
	}
	h.seedAmbientSecrets(extraEnv)
	h.writeWorkspaceMiseFile(t)
	h.Pulumi(t, nil, "login", runBackend)
	t.Cleanup(func() { h.cleanup(t) })
	return h
}

// SkipIfKeep skips destructive teardown subtests when OPENBASE_E2E_KEEP=1 so live deployments remain inspectable.
func (h *LiveStackHarness) SkipIfKeep(t *testing.T) {
	t.Helper()
	if h.Keep {
		t.Skip("skipping destructive preview teardown check because OPENBASE_E2E_KEEP=1 is set")
	}
}

// RecordLiveURL records a deployed stack URL for retained-run output and run-manifest.json.
func (h *LiveStackHarness) RecordLiveURL(stack, rawURL string) {
	if rawURL == "" {
		return
	}
	h.liveURLs[stack] = rawURL
	h.saveManifest(false)
}

// RecordVercelBlobStore records non-secret Vercel IDs so e2e:cleanup can purge blobs before destroying shared.
func (h *LiveStackHarness) RecordVercelBlobStore(projectID, teamID, blobBaseURL string) {
	h.vercelProjectID = projectID
	h.vercelTeamID = teamID
	h.blobBaseURL = blobBaseURL
	h.saveManifest(false)
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

// InitStack initializes stack and records it in run-manifest.json for reverse-order cleanup.
func (h *LiveStackHarness) InitStack(t *testing.T, stack string) {
	t.Helper()
	if !h.touched[stack] {
		h.touched[stack] = true
		h.order = append(h.order, stack)
	}
	h.Pulumi(t, nil, "stack", "init", stack, "--non-interactive")
	h.saveManifest(false)
}

// Pulumi executes a pulumi CLI command expected to succeed and appends to commands.log.
func (h *LiveStackHarness) Pulumi(t *testing.T, callEnv []string, args ...string) string {
	t.Helper()
	h.seedEnvSliceSecrets(callEnv)
	workDir := h.dirForStack(stackFromArgs(args))
	out := runCommandEnv(t, workDir, h.env(callEnv), "pulumi", args...)
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
	workDir := h.dirForStack(stackFromArgs(args))
	out := commandFailureEnv(t, workDir, h.env(callEnv), "pulumi", args...)
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
	"KINDE_E2E_PASSWORD",
	"AGENTMAIL_API_KEY",
	"OPENBASE_TEST_SECRET_VALUE",
}

func prepareBackendPath(t *testing.T, outDir, baseBackend string) string {
	t.Helper()
	if strings.TrimSpace(baseBackend) == "" {
		stateDir := filepath.Join(outDir, "state")
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		return "file://" + filepath.ToSlash(stateDir)
	}
	runBackend := fmt.Sprintf("%s/run-%d", strings.TrimRight(baseBackend, "/"), time.Now().UnixNano())
	if strings.HasPrefix(runBackend, "file://") {
		if err := os.MkdirAll(strings.TrimPrefix(runBackend, "file://"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return runBackend
}

func prepareArtifactDir(t *testing.T, suiteTest string) string {
	t.Helper()
	outDir := filepath.Join(repositoryRoot(t), "e2e", "outputs", "components", suiteTest)
	assertNoUncleanManifest(t, outDir)
	if err := os.RemoveAll(outDir); err != nil {
		t.Fatalf("clean artifact dir: %v", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	return outDir
}

func assertNoUncleanManifest(t *testing.T, outDir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(outDir, "run-manifest.json"))
	if err != nil {
		return
	}
	var m RunManifest
	if json.Unmarshal(raw, &m) == nil && !m.CleanedUp && len(m.Stacks) > 0 {
		t.Fatalf(
			"leftover live stacks found in %s (%s); run 'mise run e2e:cleanup' before re-running",
			outDir,
			strings.Join(m.Stacks, ", "),
		)
	}
}

func (h *LiveStackHarness) writeWorkspaceMiseFile(t *testing.T) {
	t.Helper()
	var b strings.Builder
	b.WriteString("[env]\n")
	b.WriteString("GOWORK = \"off\"\n")
	_, _ = fmt.Fprintf(&b, "PULUMI_HOME = %q\n", h.PulumiHome)
	_, _ = fmt.Fprintf(&b, "PULUMI_BACKEND_URL = %q\n", h.BackendURL)
	b.WriteString("PULUMI_CONFIG_PASSPHRASE = \"\"\n")
	b.WriteString("PULUMI_SKIP_UPDATE_CHECK = \"true\"\n")
	nonSecret := nonSecretEnvMap(h.extraEnv)
	keys := make([]string, 0, len(nonSecret))
	for k := range nonSecret {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		_, _ = fmt.Fprintf(&b, "%s = %q\n", k, nonSecret[k])
	}
	_ = os.WriteFile(filepath.Join(h.Dir, "mise.toml"), []byte(b.String()), 0o644)
	premiseManifest := filepath.Join(h.Dir, "premise.yaml")
	if raw, err := os.ReadFile(premiseManifest); err == nil {
		rendered := strings.ReplaceAll(string(raw), "{{BACKEND_URL}}", h.BackendURL)
		_ = os.WriteFile(premiseManifest, []byte(rendered), 0o644)
	}
}

func (h *LiveStackHarness) dirForStack(stack string) string {
	if stack == "shared" {
		sharedDir := filepath.Join(h.Dir, "infra", "shared")
		if _, err := os.Stat(filepath.Join(sharedDir, "Pulumi.yaml")); err == nil {
			return sharedDir
		}
	}
	infraDir := filepath.Join(h.Dir, "infra")
	if _, err := os.Stat(filepath.Join(infraDir, "Pulumi.yaml")); err == nil {
		return infraDir
	}
	return h.Dir
}

func stackFromArgs(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--stack" || args[i] == "-s" {
			return args[i+1]
		}
	}
	if len(args) >= 3 && args[0] == "stack" && (args[1] == "init" || args[1] == "select" || args[1] == "rm") {
		return args[2]
	}
	return ""
}

func (h *LiveStackHarness) saveManifest(cleanedUp bool) {
	m := RunManifest{
		TestName:        h.TestName,
		Fixture:         h.Fixture,
		Project:         h.Project,
		WorkspaceDir:    h.Dir,
		PulumiHome:      h.PulumiHome,
		BackendURL:      h.BackendURL,
		Stacks:          append([]string{}, h.order...),
		Env:             nonSecretEnvMap(h.extraEnv),
		LiveURLs:        h.liveURLs,
		VercelProjectID: h.vercelProjectID,
		VercelTeamID:    h.vercelTeamID,
		BlobBaseURL:     h.blobBaseURL,
		CleanedUp:       cleanedUp,
		UpdatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(h.OutputDir, "run-manifest.json"), append(raw, '\n'), 0o644)
}

func nonSecretEnvMap(entries []string) map[string]string {
	out := map[string]string{}
	for _, kv := range entries {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || v == "" || isSensitiveKey(k) {
			continue
		}
		out[k] = v
	}
	return out
}

func isSensitiveKey(key string) bool {
	u := strings.ToUpper(key)
	return strings.Contains(u, "SECRET") || strings.Contains(u, "TOKEN") || strings.Contains(u, "PASSWORD") || strings.Contains(u, "KEY")
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
		if ok && v != "" && isSensitiveKey(k) {
			h.RegisterSecret(v)
		}
	}
}

func (h *LiveStackHarness) maybeRegisterSecretOutput(args []string, val string) {
	if val == "" || len(args) < 3 {
		return
	}
	lowerKey := strings.ToLower(args[2])
	if isSensitiveKey(lowerKey) || strings.HasPrefix(val, "postgres") {
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
	return slices.Contains(args, target)
}

func isLiveEnabled(flag string) bool {
	return os.Getenv("OPENBASE_TEST_LIVE") == "1" || os.Getenv(flag) == "1"
}

func isKeepEnabled() bool {
	return os.Getenv("OPENBASE_E2E_KEEP") == "1" || os.Getenv("OPENBASE_TEST_KEEP") == "1"
}

func optionalBackendEnv() string {
	return strings.TrimRight(os.Getenv("PULUMI_BACKEND_URL"), "/")
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
	if h.Keep {
		h.saveManifest(false)
		h.logRetainedSummary(t)
		return
	}
	allOK := true
	for i := len(h.order) - 1; i >= 0; i-- {
		if !h.destroyStackForCleanup(t, h.order[i]) {
			allOK = false
		}
	}
	if allOK {
		h.saveManifest(true)
	}
	for _, verify := range h.verifiers {
		verify(t)
	}
}

func (h *LiveStackHarness) logRetainedSummary(t *testing.T) {
	t.Helper()
	t.Logf("=== RETAINED LIVE E2E ENVIRONMENT (%s) ===", h.TestName)
	t.Logf("Workspace : %s", h.Dir)
	t.Logf("State     : %s", h.BackendURL)
	t.Logf("Stacks    : %s", strings.Join(h.order, ", "))
	for _, stack := range h.order {
		if u, ok := h.liveURLs[stack]; ok {
			t.Logf("URL [%s] : %s", stack, u)
		}
	}
	t.Logf("Cleanup   : mise run e2e:cleanup")
}

func (h *LiveStackHarness) destroyStackForCleanup(t *testing.T, stack string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	workDir := h.dirForStack(stack)
	baseEnv := append(os.Environ(), h.env(nil)...)
	unprotect := exec.CommandContext(ctx, "pulumi", "state", "unprotect", "--all", "--yes", "--stack", stack)
	unprotect.Dir = workDir
	unprotect.Env = baseEnv
	_ = unprotect.Run()

	destroy := exec.CommandContext(ctx, "pulumi", "destroy", "--stack", stack, "--yes", "--non-interactive", "--skip-preview")
	destroy.Dir = workDir
	destroy.Env = baseEnv
	if out, err := destroy.CombinedOutput(); err != nil {
		t.Errorf("cleanup destroy stack %s: %v\n%s", stack, err, h.redact(string(out)))
		return false
	}
	rm := exec.CommandContext(ctx, "pulumi", "stack", "rm", stack, "--yes", "--non-interactive")
	rm.Dir = workDir
	rm.Env = baseEnv
	_ = rm.Run()
	return true
}
