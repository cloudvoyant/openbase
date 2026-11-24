// Package vercelclient provides a lightweight Vercel REST API client for E2E verification.
package vercelclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Public API -----------------------------------------------------------------

// DetectTeam returns (teamID, teamSlug) from VERCEL_TEAM_ID/SLUG or the Vercel /v2/teams API.
func DetectTeam(t *testing.T) (string, string) {
	t.Helper()
	if id, slug := os.Getenv("VERCEL_TEAM_ID"), os.Getenv("VERCEL_TEAM_SLUG"); id != "" && slug != "" {
		return id, slug
	}
	raw := doGet(t, "https://api.vercel.com/v2/teams")
	var payload struct {
		Teams []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"teams"`
	}
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.NotEmpty(t, payload.Teams, "no Vercel teams returned for VERCEL_TOKEN")
	return payload.Teams[0].ID, payload.Teams[0].Slug
}

// AssertProjectDeleted verifies the Vercel project returns 404 after stack teardown.
func AssertProjectDeleted(t *testing.T, teamID, projectID string) {
	t.Helper()
	if projectID == "" {
		return
	}
	status := doStatus(t, fmt.Sprintf("https://api.vercel.com/v9/projects/%s?teamId=%s", projectID, teamID))
	require.Equal(t, http.StatusNotFound, status, "expected Vercel project %s deleted (404)", projectID)
}

// Private helpers ------------------------------------------------------------

func doGet(t *testing.T, endpoint string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("VERCEL_TOKEN"))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	require.Less(t, resp.StatusCode, 300, "GET %s HTTP %d: %s", endpoint, resp.StatusCode, string(raw))
	return raw
}

func doStatus(t *testing.T, endpoint string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("VERCEL_TOKEN"))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}
