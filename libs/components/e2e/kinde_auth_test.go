package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

// Public API -----------------------------------------------------------------

func TestKindeAuth(t *testing.T) {
	baseBackend := RequireLiveKinde(t)
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "obkd" + runID
	devSandboxID, devSandboxName := prepareKindeSandboxApp(t, "dev", "openbase-"+appProject, appProject, "cloudvoyant")
	prodSandboxID, _ := prepareKindeSandboxApp(t, "prod", "openbase-"+appProject, appProject, "cloudvoyant")

	h := NewLiveHarness(
		t,
		"kinde-auth",
		"openbase-kinde-auth-fixture",
		baseBackend,
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_TEAM_SLUG=cloudvoyant",
		"OPENBASE_TEST_KINDE_APP_NAME="+devSandboxName,
		"OPENBASE_TEST_IMPORT_DEV_APP_ID="+devSandboxID,
		"OPENBASE_TEST_IMPORT_PROD_APP_ID="+prodSandboxID,
		"OPENBASE_TEST_RETAIN_KINDE_APPS=1",
	)

	var devAppID, prodAppID, devIssuer, prodIssuer, devClientID, prodClientID, previewRedirect string

	t.Run("when planning and applying SharedAuth + Auth across dev, preview, stage, and prod", func(t *testing.T) {
		// Arrange.
		for _, s := range []string{"shared", "dev", "preview-pr-5", "stage", "prod"} {
			h.InitStack(t, s)
		}

		// Act.
		planOut := h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=a"}, "preview", "--stack", "shared")
		h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=a"}, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
		devAppID = h.Output(t, "shared", "devAppId")
		prodAppID = h.Output(t, "shared", "prodAppId")
		devClientIDBefore := h.Output(t, "shared", "devClientId")

		h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=b"}, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
		devIssuer = h.Output(t, "shared", "devIssuerUrl")
		devClientID = h.Output(t, "shared", "devClientId")
		devClientSec := h.Output(t, "shared", "devClientSecret")
		prodIssuer = h.Output(t, "shared", "prodIssuerUrl")
		prodClientID = h.Output(t, "shared", "prodClientId")
		prodClientSec := h.Output(t, "shared", "prodClientSecret")
		emailConn := h.Output(t, "shared", "emailPasswordConn")

		sharedOutEnv := []string{
			"OPENBASE_TEST_DEV_ISSUER=" + devIssuer,
			"OPENBASE_TEST_DEV_CLIENT_ID=" + devClientID,
			"OPENBASE_TEST_DEV_CLIENT_SECRET=" + devClientSec,
			"OPENBASE_TEST_PROD_ISSUER=" + prodIssuer,
			"OPENBASE_TEST_PROD_CLIENT_ID=" + prodClientID,
			"OPENBASE_TEST_PROD_CLIENT_SECRET=" + prodClientSec,
		}
		for _, s := range []string{"dev", "preview-pr-5", "stage", "prod"} {
			h.Pulumi(t, sharedOutEnv, "up", "--stack", s, "--yes", "--non-interactive", "--skip-preview")
		}
		previewRedirect = h.Output(t, "preview-pr-5", "redirectUrl")

		// Assert.
		t.Run("it creates/updates dev and prod Kinde Applications in-place and attaches email:password on the dev tenant", func(t *testing.T) {
			assertContains(t, planOut, "kinde:index:Application")
			assertEqualStr(t, "in-place devClientId", devClientID, devClientIDBefore)
			if emailConn == "" {
				t.Fatal("expected email:password connection ID on Kinde dev tenant")
			}
			assertAuthorizeAllowed(t, devIssuer, devClientID, "http://localhost:4173/api/auth/kinde_callback")
		})

		t.Run("it routes dev/preview to dev tenant creds and stage/prod to prod tenant creds", func(t *testing.T) {
			assertEqualStr(t, "dev issuer", h.Output(t, "dev", "issuerUrl"), devIssuer)
			assertEqualStr(t, "preview issuer", h.Output(t, "preview-pr-5", "issuerUrl"), devIssuer)
			assertEqualStr(t, "preview clientId", h.Output(t, "preview-pr-5", "clientId"), devClientID)
			assertEqualStr(t, "stage issuer", h.Output(t, "stage", "issuerUrl"), prodIssuer)
			assertEqualStr(t, "prod issuer", h.Output(t, "prod", "issuerUrl"), prodIssuer)
			assertEqualStr(t, "prod clientId", h.Output(t, "prod", "clientId"), prodClientID)
		})

		t.Run("it returns 302 to login on /oauth2/auth for allowed redirect_uris and rejects unallowed redirect_uris", func(t *testing.T) {
			assertOIDCDiscovery(t, devIssuer)
			assertOIDCDiscovery(t, prodIssuer)
			assertAuthorizeAllowed(t, devIssuer, devClientID, previewRedirect)
			assertAuthorizeRejected(t, devIssuer, devClientID, "https://unregistered.example.com/callback")
		})
	})

	t.Run("when destroying preview", func(t *testing.T) {
		// Arrange / Act.
		h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-5", "--yes", "--non-interactive", "--skip-preview")
		h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-5", "--yes", "--non-interactive", "--skip-preview")

		// Assert.
		t.Run("it leaves shared dev and prod Kinde Applications active for remaining stacks", func(t *testing.T) {
			assertKindeAppStatus(t, "dev", devAppID, http.StatusOK)
			assertKindeAppStatus(t, "prod", prodAppID, http.StatusOK)
		})
	})

	h.OnCleanupVerify(func(t *testing.T) {
		if countOpenBaseKindeApps(t, "dev") > 1 || countOpenBaseKindeApps(t, "prod") > 1 {
			t.Fatal("expected at most 1 retained openbase-e2e Kinde sandbox app per tenant")
		}
	})
}

// Private helpers ------------------------------------------------------------

func prepareKindeSandboxApp(t *testing.T, tenant, vProj, appProject, teamSlug string) (string, string) {
	t.Helper()
	issuer, tok := kindeMgmtToken(t, tenant)
	apps := listOpenBaseKindeApps(t, issuer, tok)
	if len(apps) == 0 {
		return "", "openbase-e2e-" + appProject
	}
	id, name := apps[0].ID, apps[0].Name
	redirects, logouts := expectedInitialKindeURLs(tenant, vProj, teamSlug)
	putKindeURLs(t, issuer, tok, id, "auth_redirect_urls", redirects)
	putKindeURLs(t, issuer, tok, id, "auth_logout_urls", logouts)
	return id, name
}

func expectedInitialKindeURLs(tenant, vProj, teamSlug string) ([]string, []string) {
	if tenant == "dev" {
		return []string{
			"http://localhost:3000/api/auth/kinde_callback",
			"http://localhost:5173/api/auth/kinde_callback",
			fmt.Sprintf("https://%s-*-%s.vercel.app/api/auth/kinde_callback", vProj, teamSlug),
		}, []string{
			"http://localhost:3000/",
			"http://localhost:5173/",
			fmt.Sprintf("https://%s-*-%s.vercel.app/", vProj, teamSlug),
		}
	}
	return []string{
		fmt.Sprintf("https://%s-%s.vercel.app/api/auth/kinde_callback", vProj, teamSlug),
		fmt.Sprintf("https://%s-stage-%s.vercel.app/api/auth/kinde_callback", vProj, teamSlug),
	}, []string{
		fmt.Sprintf("https://%s-%s.vercel.app/", vProj, teamSlug),
		fmt.Sprintf("https://%s-stage-%s.vercel.app/", vProj, teamSlug),
	}
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

type kindeAppItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func countOpenBaseKindeApps(t *testing.T, tenant string) int {
	t.Helper()
	issuer, tok := kindeMgmtToken(t, tenant)
	return len(listOpenBaseKindeApps(t, issuer, tok))
}

func listOpenBaseKindeApps(t *testing.T, issuer, tok string) []kindeAppItem {
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
		if strings.HasPrefix(a.Name, "openbase-e2e-") {
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

func assertOIDCDiscovery(t *testing.T, issuer string) {
	t.Helper()
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Get(issuer + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("OIDC discovery %s: %v", issuer, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OIDC discovery %s status = %d", issuer, resp.StatusCode)
	}
}

func assertAuthorizeAllowed(t *testing.T, issuer, clientID, redirectURI string) {
	t.Helper()
	status, loc, _ := callKindeAuthorize(t, issuer, clientID, redirectURI)
	if (status != http.StatusFound && status != http.StatusSeeOther) || strings.Contains(strings.ToLower(loc), "error=") {
		t.Fatalf("expected 302 login redirect for %s, got status=%d loc=%q", redirectURI, status, loc)
	}
}

func assertAuthorizeRejected(t *testing.T, issuer, clientID, redirectURI string) {
	t.Helper()
	status, loc, body := callKindeAuthorize(t, issuer, clientID, redirectURI)
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

func assertKindeAppStatus(t *testing.T, tenant, appID string, want int) {
	t.Helper()
	issuer, tok := kindeMgmtToken(t, tenant)
	req, err := http.NewRequest(http.MethodGet, issuer+"/api/v1/applications/"+appID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		t.Fatalf("Kinde %s app %s status = %d, want %d", tenant, appID, resp.StatusCode, want)
	}
}

func kindeMgmtEnv(tenant string) (string, string, string) {
	if tenant == "prod" {
		return kinde.NormalizeIssuerURL(os.Getenv("KINDE_PROD_TENANT_DOMAIN")),
			os.Getenv("KINDE_PROD_M2M_CLIENT_ID"),
			os.Getenv("KINDE_PROD_M2M_CLIENT_SECRET")
	}
	return kinde.NormalizeIssuerURL(os.Getenv("KINDE_DEV_TENANT_DOMAIN")),
		os.Getenv("KINDE_DEV_M2M_CLIENT_ID"),
		os.Getenv("KINDE_DEV_M2M_CLIENT_SECRET")
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
