package neon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Private helpers ------------------------------------------------------------

const defaultNeonAPIBaseURL = "https://console.neon.tech/api/v2"

type revealPasswordPayload struct {
	Password string `json:"password"`
}

func revealRolePasswordOutput(
	ctx *pulumi.Context,
	projectID, branchID pulumi.StringOutput,
	roleName string,
	apiKeyInput pulumi.StringInput,
	apiBaseURL string,
) pulumi.StringOutput {
	if ctx.DryRun() || apiKeyInput == nil {
		raw := pulumi.All(projectID, branchID).ApplyT(func(_ []any) string {
			return "mock-neon-role-password"
		}).(pulumi.StringOutput)
		return pulumi.ToSecret(raw).(pulumi.StringOutput)
	}
	raw := pulumi.All(projectID, branchID, apiKeyInput.ToStringOutput()).ApplyTWithContext(
		context.Background(),
		func(goCtx context.Context, vs []any) (string, error) {
			apiKey := strings.TrimSpace(vs[2].(string))
			if apiKey == "" {
				return "mock-neon-role-password", nil
			}
			return fetchNeonRolePassword(goCtx, apiKey, apiBaseURL, vs[0].(string), vs[1].(string), roleName)
		},
	).(pulumi.StringOutput)
	return pulumi.ToSecret(raw).(pulumi.StringOutput)
}

func fetchNeonRolePassword(ctx context.Context, apiKey, apiBaseURL, projectID, branchID, roleName string) (string, error) {
	baseURL := strings.TrimRight(defaultIdentifier(apiBaseURL, defaultNeonAPIBaseURL), "/")
	endpoint := fmt.Sprintf(
		"%s/projects/%s/branches/%s/roles/%s/reveal_password",
		baseURL,
		url.PathEscape(projectID),
		url.PathEscape(branchID),
		url.PathEscape(roleName),
	)
	client := &http.Client{Timeout: 15 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		pw, retry, err := attemptRevealRolePassword(ctx, client, endpoint, apiKey)
		if err == nil {
			return pw, nil
		}
		lastErr = err
		if !retry {
			return "", err
		}
		time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
	return "", lastErr
}

func attemptRevealRolePassword(ctx context.Context, client *http.Client, endpoint, apiKey string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", true, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusLocked || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return "", retry, fmt.Errorf("neon reveal_password returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload revealPasswordPayload
	if err := json.Unmarshal(body, &payload); err != nil || payload.Password == "" {
		return "", false, errors.New("neon reveal_password returned an empty role password")
	}
	return payload.Password, false, nil
}
