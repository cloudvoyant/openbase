package goutils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Public API -----------------------------------------------------------------

// KindeApp represents an OIDC application returned by the Kinde Management API.
type KindeApp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// KindeMgmtToken exchanges Kinde M2M credentials for a Management API access token.
func KindeMgmtToken(ctx context.Context, issuer, clientID, clientSecret string) (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"audience":      {strings.TrimRight(issuer, "/") + "/api"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(issuer, "/")+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("kinde token HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

// ListKindeApps returns all Kinde applications in the given environment.
func ListKindeApps(ctx context.Context, issuer, token string) ([]KindeApp, error) {
	raw, err := kindeDoJSON(ctx, http.MethodGet, strings.TrimRight(issuer, "/")+"/api/v1/applications?page_size=200", token, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Applications []KindeApp `json:"applications"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload.Applications, nil
}

// DeleteKindeApp deletes a Kinde application by ID.
func DeleteKindeApp(ctx context.Context, issuer, token, appID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/api/v1/applications/%s", strings.TrimRight(issuer, "/"), appID), nil)
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

// SetKindeAppURLs updates auth redirect and logout callback allowlists on a Kinde application.
func SetKindeAppURLs(ctx context.Context, issuer, token, appID string, redirects, logouts []string) error {
	base := fmt.Sprintf("%s/api/v1/applications/%s", strings.TrimRight(issuer, "/"), appID)
	if _, err := kindeDoJSON(ctx, http.MethodPut, base+"/auth_redirect_urls", token, map[string]any{"urls": redirects}); err != nil {
		return err
	}
	_, err := kindeDoJSON(ctx, http.MethodPut, base+"/auth_logout_urls", token, map[string]any{"urls": logouts})
	return err
}

// Private helpers ------------------------------------------------------------

func kindeDoJSON(ctx context.Context, method, endpoint, token string, body any) ([]byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s HTTP %d: %s", method, endpoint, resp.StatusCode, string(raw))
	}
	return raw, nil
}
