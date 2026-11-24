// Package kindeclient provides a lightweight Kinde Management API HTTP client for E2E verification.
package kindeclient

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

	"github.com/stretchr/testify/require"

	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/testutils/liveenv"
)

// Public API -----------------------------------------------------------------

// App represents a Kinde OIDC application returned by the Management API.
type App struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MgmtToken exchanges Kinde M2M credentials for an access token for the given environment ("non-prod" or "prod").
func MgmtToken(t *testing.T, envName string) (string, string) {
	t.Helper()
	issuer, clientID, clientSecret := mgmtEnv(envName)
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"audience":      {issuer + "/api"},
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).PostForm(issuer+"/oauth2/token", form)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	require.Less(t, resp.StatusCode, 300, "kinde %s token HTTP %d: %s", envName, resp.StatusCode, string(raw))
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(raw, &tok))
	return issuer, tok.AccessToken
}

// ListApps returns all Kinde applications in the specified environment.
func ListApps(t *testing.T, issuer, token string) []App {
	t.Helper()
	raw := doJSON(t, http.MethodGet, issuer+"/api/v1/applications?page_size=200", token, nil)
	var payload struct {
		Applications []App `json:"applications"`
	}
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload.Applications
}

// CountByPrefix returns the number of Kinde apps whose name starts with prefix.
func CountByPrefix(t *testing.T, envName, prefix string) int {
	t.Helper()
	issuer, tok := MgmtToken(t, envName)
	count := 0
	for _, app := range ListApps(t, issuer, tok) {
		if strings.HasPrefix(app.Name, prefix) {
			count++
		}
	}
	return count
}

// PrepareSandboxApp deletes stale duplicate E2E apps matching prefix and returns one reusable app (or empty for create).
func PrepareSandboxApp(t *testing.T, envName, prefix, fallbackName string, redirects, logouts []string) (string, string) {
	t.Helper()
	issuer, tok := MgmtToken(t, envName)
	var kept *App
	for _, app := range ListApps(t, issuer, tok) {
		if !strings.HasPrefix(app.Name, prefix) {
			continue
		}
		if kept == nil {
			cp := app
			kept = &cp
			continue
		}
		_ = deleteApp(issuer, tok, app.ID)
	}
	if kept == nil {
		return "", fallbackName
	}
	seedCallbacks(t, issuer, tok, kept.ID, redirects, logouts)
	return kept.ID, kept.Name
}

// Private helpers ------------------------------------------------------------

func mgmtEnv(envName string) (string, string, string) {
	if envName == "prod" {
		return kinde.NormalizeIssuerURL(os.Getenv("KINDE_PROD_TENANT_DOMAIN")),
			os.Getenv("KINDE_PROD_M2M_CLIENT_ID"),
			os.Getenv("KINDE_PROD_M2M_CLIENT_SECRET")
	}
	dom := liveenv.FirstNonEmpty(os.Getenv("KINDE_NONPROD_TENANT_DOMAIN"), os.Getenv("KINDE_DEV_TENANT_DOMAIN"))
	cid := liveenv.FirstNonEmpty(os.Getenv("KINDE_NONPROD_M2M_CLIENT_ID"), os.Getenv("KINDE_DEV_M2M_CLIENT_ID"))
	sec := liveenv.FirstNonEmpty(os.Getenv("KINDE_NONPROD_M2M_CLIENT_SECRET"), os.Getenv("KINDE_DEV_M2M_CLIENT_SECRET"))
	return kinde.NormalizeIssuerURL(dom), cid, sec
}

func doJSON(t *testing.T, method, endpoint, token string, body any) []byte {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, endpoint, r)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	require.Less(t, resp.StatusCode, 300, "%s %s HTTP %d: %s", method, endpoint, resp.StatusCode, string(raw))
	return raw
}

func deleteApp(issuer, token, id string) error {
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/applications/%s", issuer, id), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func seedCallbacks(t *testing.T, issuer, token, appID string, redirects, logouts []string) {
	t.Helper()
	doJSON(t, http.MethodPut, fmt.Sprintf("%s/api/v1/applications/%s/auth_redirect_urls", issuer, appID), token, map[string]any{
		"urls": redirects,
	})
	doJSON(t, http.MethodPut, fmt.Sprintf("%s/api/v1/applications/%s/auth_logout_urls", issuer, appID), token, map[string]any{
		"urls": logouts,
	})
}
