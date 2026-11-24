// Package neonclient provides a lightweight Neon REST API client for E2E verification.
package neonclient

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

// DetectOrgID returns NEON_ORG_ID or discovers the first Neon organization for NEON_API_KEY.
func DetectOrgID(t *testing.T) string {
	t.Helper()
	if orgID := os.Getenv("NEON_ORG_ID"); orgID != "" {
		return orgID
	}
	req, err := http.NewRequest(http.MethodGet, "https://console.neon.tech/api/v2/users/me/organizations", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("NEON_API_KEY"))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	require.Less(t, resp.StatusCode, 300, "neon organizations HTTP %d: %s", resp.StatusCode, string(raw))
	var payload struct {
		Organizations []struct {
			ID string `json:"id"`
		} `json:"organizations"`
	}
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.NotEmpty(t, payload.Organizations, "no Neon organizations returned for NEON_API_KEY")
	return payload.Organizations[0].ID
}

// AssertProjectDeleted verifies the Neon project returns 404 after stack teardown.
func AssertProjectDeleted(t *testing.T, projectID string) {
	t.Helper()
	if projectID == "" {
		return
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s", projectID), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("NEON_API_KEY"))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "expected Neon project %s deleted (404)", projectID)
}
