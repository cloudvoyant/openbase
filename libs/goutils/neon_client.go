package goutils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Public API -----------------------------------------------------------------

// DetectNeonOrgID queries the Neon v2 API with apiKey and returns the first organization ID.
func DetectNeonOrgID(ctx context.Context, apiKey string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://console.neon.tech/api/v2/users/me/organizations", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("neon organizations HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var payload struct {
		Organizations []struct {
			ID string `json:"id"`
		} `json:"organizations"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	if len(payload.Organizations) == 0 {
		return "", errors.New("no Neon organizations returned for API key")
	}
	return payload.Organizations[0].ID, nil
}

// GetNeonProjectStatus returns the HTTP status code for GET /api/v2/projects/<projectID>.
func GetNeonProjectStatus(ctx context.Context, apiKey, projectID string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s", projectID), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
