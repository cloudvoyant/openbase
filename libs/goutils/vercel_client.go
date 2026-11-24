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

// DetectVercelTeam queries the Vercel /v2/teams API with token and returns (teamID, teamSlug).
func DetectVercelTeam(ctx context.Context, token string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.vercel.com/v2/teams", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("vercel teams HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var payload struct {
		Teams []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", err
	}
	if len(payload.Teams) == 0 {
		return "", "", errors.New("no Vercel teams returned for token")
	}
	return payload.Teams[0].ID, payload.Teams[0].Slug, nil
}

// GetVercelProjectStatus returns the HTTP status code for GET /v9/projects/<projectID>.
func GetVercelProjectStatus(ctx context.Context, token, teamID, projectID string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.vercel.com/v9/projects/%s?teamId=%s", projectID, teamID), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
