package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/jackc/pgx/v5"

	"github.com/cloudvoyant/openbase/libs/components/gcp/overlayfs"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Private helpers ------------------------------------------------------------

type gcsBucketMetadata struct {
	Labels                   map[string]string `json:"labels"`
	VersioningEnabled        bool              `json:"versioning_enabled"`
	PublicAccessPrevention   string            `json:"public_access_prevention"`
	UniformBucketLevelAccess bool              `json:"uniform_bucket_level_access"`
	SoftDeletePolicy         struct {
		RetentionDurationSeconds string `json:"retentionDurationSeconds"`
	} `json:"soft_delete_policy"`
}

type overlayFSReport struct {
	FallbackSource   string `json:"fallbackSource"`
	FallbackContent  string `json:"fallbackContent"`
	OverrideSource   string `json:"overrideSource"`
	OverrideContent  string `json:"overrideContent"`
	DevSeedAfter     string `json:"devSeedAfter"`
	DeltaAbsentInDev bool   `json:"deltaAbsentInDev"`
}

type kindeAppItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}

func assertEqualStr(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func describeGCSBucket(t *testing.T, bucketName string) gcsBucketMetadata {
	t.Helper()
	raw := runCommand(t, repositoryRoot(t), "gcloud", "storage", "buckets", "describe", "gs://"+bucketName, "--format=json")
	var meta gcsBucketMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("unmarshal bucket metadata: %v\n%s", err, raw)
	}
	return meta
}

func assertProtectedBucketFields(t *testing.T, bucketName string, meta gcsBucketMetadata) {
	t.Helper()
	if !meta.UniformBucketLevelAccess {
		t.Fatalf("expected uniform_bucket_level_access on %s", bucketName)
	}
	assertEqualStr(t, "public_access_prevention", meta.PublicAccessPrevention, "enforced")
	if !meta.VersioningEnabled {
		t.Fatalf("expected versioning_enabled on %s", bucketName)
	}
	assertEqualStr(t, "soft_delete_policy", meta.SoftDeletePolicy.RetentionDurationSeconds, "604800")
}

func assertBucketsAbsent(t *testing.T, names ...string) {
	t.Helper()
	for _, b := range names {
		if b != "" {
			assertBucketAbsent(t, b)
		}
	}
}

func assertBucketAbsent(t *testing.T, bucketName string) {
	t.Helper()
	out := commandFailureEnv(t, repositoryRoot(t), nil, "gcloud", "storage", "buckets", "describe", "gs://"+bucketName)
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "not found") && !strings.Contains(lower, "404") {
		t.Fatalf("expected 404/not found for gs://%s, got:\n%s", bucketName, out)
	}
}

func seedGCSObject(t *testing.T, bucketName, key, content string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()
	store, err := overlayfs.NewStore(client, bucketName, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, key, []byte(content), "text/plain"); err != nil {
		t.Fatalf("seed bucket %s: %v", bucketName, err)
	}
}

func exerciseOverlayFS(t *testing.T, devBucket, previewBucket, previewFallback string) overlayFSReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()
	devStore, err := overlayfs.NewStore(client, devBucket, "")
	if err != nil {
		t.Fatal(err)
	}
	previewStore, err := overlayfs.NewStore(client, previewBucket, previewFallback)
	if err != nil {
		t.Fatal(err)
	}
	return runOverlayOperations(t, ctx, devStore, previewStore)
}

func runOverlayOperations(t *testing.T, ctx context.Context, devStore, previewStore *overlayfs.Store) overlayFSReport {
	t.Helper()
	seedBytes, src, err := previewStore.Read(ctx, "seed.txt")
	if err != nil {
		t.Fatalf("overlay fallback read: %v", err)
	}
	if err := previewStore.Write(ctx, "overlay-delta.txt", []byte("only-preview"), "text/plain"); err != nil {
		t.Fatalf("preview write delta: %v", err)
	}
	if err := previewStore.Write(ctx, "seed.txt", []byte("preview-override"), "text/plain"); err != nil {
		t.Fatalf("preview override seed: %v", err)
	}
	overBytes, overSrc, err := previewStore.Read(ctx, "seed.txt")
	if err != nil {
		t.Fatal(err)
	}
	devSeedAfter, _, err := devStore.Read(ctx, "seed.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _, deltaErr := devStore.Read(ctx, "overlay-delta.txt")
	return overlayFSReport{
		FallbackSource:   src,
		FallbackContent:  string(seedBytes),
		OverrideSource:   overSrc,
		OverrideContent:  string(overBytes),
		DevSeedAfter:     string(devSeedAfter),
		DeltaAbsentInDev: errors.Is(deltaErr, storage.ErrObjectNotExist),
	}
}

func assertCloudRunServicesAbsent(t *testing.T, gcpProject string, names ...string) {
	t.Helper()
	for _, svc := range names {
		if svc != "" {
			assertCloudRunAbsent(t, gcpProject, svc)
		}
	}
}

func assertCloudRunAbsent(t *testing.T, gcpProject, serviceName string) {
	t.Helper()
	out := commandFailureEnv(
		t,
		repositoryRoot(t),
		nil,
		"gcloud", "run", "services", "describe", serviceName,
		"--region=us-central1",
		"--project="+gcpProject,
	)
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "cannot find") && !strings.Contains(lower, "not found") && !strings.Contains(lower, "404") {
		t.Fatalf("expected Cloud Run service %s absent, got:\n%s", serviceName, out)
	}
}

func assertSecretAbsent(t *testing.T, gcpProject, secretID string) {
	t.Helper()
	out := commandFailureEnv(
		t,
		repositoryRoot(t),
		nil,
		"gcloud", "secrets", "describe", secretID,
		"--project="+gcpProject,
	)
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "not found") && !strings.Contains(lower, "404") {
		t.Fatalf("expected Secret Manager secret %s absent, got:\n%s", secretID, out)
	}
}

func assertServiceAccountAbsent(t *testing.T, gcpProject, email string) {
	t.Helper()
	var listed string
	for range 8 {
		listed = strings.TrimSpace(runCommand(
			t,
			repositoryRoot(t),
			"gcloud", "iam", "service-accounts", "list",
			"--project="+gcpProject,
			"--filter=email="+email,
			"--format=value(email)",
		))
		if listed == "" {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if listed != "" {
		t.Fatalf("expected SA %s deleted, still listed: %s", email, listed)
	}
}

func detectNeonOrgID(t *testing.T) string {
	t.Helper()
	if existing := os.Getenv("NEON_ORG_ID"); existing != "" {
		return existing
	}
	req, err := http.NewRequest(http.MethodGet, "https://console.neon.tech/api/v2/users/me/organizations", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("NEON_API_KEY"))
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var parsed struct {
		Organizations []struct {
			ID string `json:"id"`
		} `json:"organizations"`
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &parsed)
	if len(parsed.Organizations) > 0 {
		return parsed.Organizations[0].ID
	}
	return ""
}

func querySingleText(t *testing.T, connURL, sqlQuery string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, connURL)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var out string
	if err := conn.QueryRow(ctx, sqlQuery).Scan(&out); err != nil {
		t.Fatalf("queryRow: %v", err)
	}
	return out
}

func assertNeonStatus(t *testing.T, targetURL string, want int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("NEON_API_KEY"))
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Neon API GET %s: %v", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("Neon API GET %s status = %d, want %d (%s)", targetURL, resp.StatusCode, want, string(raw))
	}
}

func prepareKindeSandboxApp(t *testing.T, tenant, vProj, appProject, teamSlug string) (string, string) {
	t.Helper()
	redirects, logouts := expectedInitialKindeURLs(tenant, vProj, teamSlug)
	return prepareKindeSandboxByPrefix(t, tenant, "openbase-e2e-ob", "openbase-e2e-"+appProject, redirects, logouts)
}

func prepareKindeSandboxByPrefix(t *testing.T, tenant, prefix, fallbackName string, redirects, logouts []string) (string, string) {
	t.Helper()
	issuer, tok := kindeMgmtToken(t, tenant)
	apps := listKindeAppsByPrefix(t, issuer, tok, prefix)
	if len(apps) == 0 {
		return "", fallbackName
	}
	id, name := apps[0].ID, apps[0].Name
	putKindeURLs(t, issuer, tok, id, "auth_redirect_urls", redirects)
	putKindeURLs(t, issuer, tok, id, "auth_logout_urls", logouts)
	return id, name
}

func expectedInitialKindeURLs(tenant, vProj, teamSlug string) ([]string, []string) {
	if tenant != "prod" {
		redirects := []string{
			"http://localhost:3000/api/auth/kinde_callback",
			"http://localhost:5173/api/auth/kinde_callback",
			fmt.Sprintf("https://%s.vercel.app/api/auth/kinde_callback", vProj),
			fmt.Sprintf("https://%s-%s.vercel.app/api/auth/kinde_callback", vProj, teamSlug),
			fmt.Sprintf("https://%s-*-%s.vercel.app/api/auth/kinde_callback", vProj, teamSlug),
		}
		logouts := []string{
			"http://localhost:3000/",
			"http://localhost:5173/",
			fmt.Sprintf("https://%s.vercel.app/", vProj),
			fmt.Sprintf("https://%s-%s.vercel.app/", vProj, teamSlug),
			fmt.Sprintf("https://%s-*-%s.vercel.app/", vProj, teamSlug),
		}
		slices.Sort(redirects)
		slices.Sort(logouts)
		return redirects, logouts
	}
	redirects := []string{
		fmt.Sprintf("https://%s.vercel.app/api/auth/kinde_callback", vProj),
		fmt.Sprintf("https://%s-%s.vercel.app/api/auth/kinde_callback", vProj, teamSlug),
		fmt.Sprintf("https://%s-stage-%s.vercel.app/api/auth/kinde_callback", vProj, teamSlug),
	}
	logouts := []string{
		fmt.Sprintf("https://%s.vercel.app/", vProj),
		fmt.Sprintf("https://%s-%s.vercel.app/", vProj, teamSlug),
		fmt.Sprintf("https://%s-stage-%s.vercel.app/", vProj, teamSlug),
	}
	slices.Sort(redirects)
	slices.Sort(logouts)
	return redirects, logouts
}

func putKindeURLs(t *testing.T, issuer, tok, appID, endpoint string, urls []string) {
	t.Helper()
	body, _ := json.Marshal(map[string][]string{"urls": urls})
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/applications/%s/%s", issuer, appID, endpoint), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func countOpenBaseKindeApps(t *testing.T, tenant string) int {
	t.Helper()
	return countKindeAppsByPrefix(t, tenant, "openbase-e2e-ob")
}

func countKindeAppsByPrefix(t *testing.T, tenant, prefix string) int {
	t.Helper()
	issuer, tok := kindeMgmtToken(t, tenant)
	return len(listKindeAppsByPrefix(t, issuer, tok, prefix))
}

func listKindeAppsByPrefix(t *testing.T, issuer, tok, prefix string) []kindeAppItem {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, issuer+"/api/v1/applications", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Applications []kindeAppItem `json:"applications"`
	}
	_ = json.Unmarshal(raw, &parsed)
	var out []kindeAppItem
	for _, a := range parsed.Applications {
		if strings.HasPrefix(a.Name, prefix) {
			out = append(out, a)
		}
	}
	return out
}

func kindeMgmtToken(t *testing.T, tenant string) (string, string) {
	t.Helper()
	issuer, clientID, clientSecret := kindeMgmtEnv(tenant)
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"audience":      {issuer + "/api"},
	}
	tokResp, err := (&http.Client{Timeout: 15 * time.Second}).PostForm(issuer+"/oauth2/token", form)
	if err != nil {
		t.Fatalf("Kinde token request: %v", err)
	}
	defer func() { _ = tokResp.Body.Close() }()
	rawTok, _ := io.ReadAll(tokResp.Body)
	return issuer, extractJSONField(string(rawTok), "access_token")
}

func kindeMgmtEnv(tenant string) (string, string, string) {
	if tenant == "prod" {
		return kinde.NormalizeIssuerURL(os.Getenv("KINDE_PROD_TENANT_DOMAIN")),
			os.Getenv("KINDE_PROD_M2M_CLIENT_ID"),
			os.Getenv("KINDE_PROD_M2M_CLIENT_SECRET")
	}
	dom := firstNonEmpty(os.Getenv("KINDE_NONPROD_TENANT_DOMAIN"), os.Getenv("KINDE_DEV_TENANT_DOMAIN"))
	cid := firstNonEmpty(os.Getenv("KINDE_NONPROD_M2M_CLIENT_ID"), os.Getenv("KINDE_DEV_M2M_CLIENT_ID"))
	csec := firstNonEmpty(os.Getenv("KINDE_NONPROD_M2M_CLIENT_SECRET"), os.Getenv("KINDE_DEV_M2M_CLIENT_SECRET"))
	return kinde.NormalizeIssuerURL(dom), cid, csec
}

func extractJSONField(rawJSON, key string) string {
	marker := `"` + key + `":"`
	idx := strings.Index(rawJSON, marker)
	if idx < 0 {
		return ""
	}
	rest := rawJSON[idx+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func assertLoginRedirect(t *testing.T, redirectURI string, status int, loc string) {
	t.Helper()
	if (status != http.StatusFound && status != http.StatusSeeOther) || strings.Contains(strings.ToLower(loc), "error=") {
		t.Fatalf("expected 302 login redirect for %s, got status=%d loc=%q", redirectURI, status, loc)
	}
}

func assertRedirectRejected(t *testing.T, redirectURI string, status int, loc, body string) {
	t.Helper()
	if status == http.StatusFound || status == http.StatusSeeOther {
		if !strings.Contains(strings.ToLower(loc), "error") {
			t.Fatalf("expected unallowed redirect %s to be rejected, got 302 to %q", redirectURI, loc)
		}
		return
	}
	if status < 400 && !strings.Contains(strings.ToLower(body), "redirect") && !strings.Contains(strings.ToLower(body), "error") {
		t.Fatalf("expected rejection for %s, got status=%d", redirectURI, status)
	}
}

func callKindeAuthorize(t *testing.T, issuer, clientID, redirectURI string) (int, string, string) {
	t.Helper()
	q := url.Values{
		"client_id":     {clientID},
		"response_type": {"code"},
		"scope":         {"openid email"},
		"redirect_uri":  {redirectURI},
		"state":         {"openbase_e2e_state_12345"},
	}
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(issuer + "/oauth2/auth?" + q.Encode())
	if err != nil {
		t.Fatalf("GET /oauth2/auth: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(raw)
}

func assertVercelBlobCleanup(t *testing.T, teamID, projectID, storeID string) {
	t.Helper()
	if projectID != "" {
		assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v9/projects/%s?teamId=%s", projectID, teamID), http.StatusNotFound)
	}
	if storeID != "" {
		assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v1/storage/stores/%s?teamId=%s", storeID, teamID), http.StatusNotFound)
	}
}

func detectVercelTeam(t *testing.T) (string, string) {
	t.Helper()
	raw := vercelGET(t, "https://api.vercel.com/v2/teams", http.StatusOK)
	var parsed struct {
		Teams []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Teams) == 0 {
		t.Fatalf("failed to resolve Vercel team: %v", err)
	}
	return parsed.Teams[0].ID, parsed.Teams[0].Slug
}

func fetchProjectBlobRWToken(t *testing.T, teamID, projectID string) string {
	t.Helper()
	raw := vercelGET(t, fmt.Sprintf("https://api.vercel.com/v10/projects/%s/env?teamId=%s&decrypt=true", projectID, teamID), http.StatusOK)
	var list struct {
		Envs []struct {
			ID     string   `json:"id"`
			Key    string   `json:"key"`
			Value  string   `json:"value"`
			Target []string `json:"target"`
		} `json:"envs"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	for _, ev := range list.Envs {
		if tok := resolveDevBlobEnvEntry(t, teamID, projectID, ev.ID, ev.Key, ev.Value, ev.Target); tok != "" {
			return tok
		}
	}
	t.Fatalf("BLOB_READ_WRITE_TOKEN not found on project %s", projectID)
	return ""
}

func resolveDevBlobEnvEntry(t *testing.T, teamID, projectID, id, key, val string, targets []string) string {
	t.Helper()
	if key != "BLOB_READ_WRITE_TOKEN" || !slices.Contains(targets, "development") {
		return ""
	}
	if strings.HasPrefix(val, "vercel_blob_rw_") {
		return val
	}
	single := vercelGET(t, fmt.Sprintf("https://api.vercel.com/v9/projects/%s/env/%s?teamId=%s", projectID, id, teamID), http.StatusOK)
	var detail struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(single, &detail)
	if strings.HasPrefix(detail.Value, "vercel_blob_rw_") {
		return detail.Value
	}
	return ""
}

func inspectStageBlobEnvVars(t *testing.T, teamID, projectID, stageEnvID string) (bool, bool) {
	t.Helper()
	raw := vercelGET(t, fmt.Sprintf("https://api.vercel.com/v10/projects/%s/env?teamId=%s", projectID, teamID), http.StatusOK)
	var list struct {
		Envs []struct {
			Key                  string   `json:"key"`
			CustomEnvironmentIDs []string `json:"customEnvironmentIds"`
		} `json:"envs"`
	}
	_ = json.Unmarshal(raw, &list)
	var hasToken, hasBase bool
	for _, ev := range list.Envs {
		if !slices.Contains(ev.CustomEnvironmentIDs, stageEnvID) {
			continue
		}
		hasToken = hasToken || ev.Key == "BLOB_READ_WRITE_TOKEN"
		hasBase = hasBase || ev.Key == "BLOB_BASE_URL"
	}
	return hasToken, hasBase
}

func exerciseBlobOverlayClient(t *testing.T, baseURL, rwToken, devPrefix, previewPrefix, previewFallback string) (string, string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	devClient, err := vercel.NewBlobOverlayClient(baseURL, rwToken, devPrefix, "")
	if err != nil {
		t.Fatal(err)
	}
	previewClient, err := vercel.NewBlobOverlayClient(baseURL, rwToken, previewPrefix, previewFallback)
	if err != nil {
		t.Fatal(err)
	}
	seedBytes, src, err := previewClient.Get(ctx, "seed.txt")
	if err != nil {
		t.Fatalf("preview fallback read: %v", err)
	}
	if _, err := previewClient.Put(ctx, "client-delta.txt", []byte("only-in-preview"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	_, _, deltaErr := devClient.Get(ctx, "client-delta.txt")
	return src, string(seedBytes), errors.Is(deltaErr, vercel.ErrBlobNotFound)
}

func probeUnauthenticatedPreview(t *testing.T, targetURL string) (int, string) {
	t.Helper()
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(targetURL)
	if err != nil {
		t.Fatalf("GET %s: %v", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, resp.Header.Get("Location")
}

func assertPreviewStatusBlocked(t *testing.T, targetURL string, status int, loc string) {
	t.Helper()
	blocked := status == http.StatusUnauthorized ||
		status == http.StatusForbidden ||
		((status == http.StatusFound || status == http.StatusTemporaryRedirect) && strings.Contains(loc, "vercel.com"))
	if !blocked {
		t.Fatalf("expected unauthenticated preview %s blocked (401/403 or 302 to vercel.com SSO), got status=%d loc=%q", targetURL, status, loc)
	}
}

func vercelGET(t *testing.T, targetURL string, wantStatus int) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("VERCEL_TOKEN"))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Vercel GET %s: %v", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("Vercel GET %s status = %d, want %d: %s", targetURL, resp.StatusCode, wantStatus, string(raw))
	}
	return raw
}

func assertVercelStatus(t *testing.T, targetURL string, want int) {
	t.Helper()
	_ = vercelGET(t, targetURL, want)
}
