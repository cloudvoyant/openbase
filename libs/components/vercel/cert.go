package vercel

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

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Private helpers ------------------------------------------------------------

const (
	defaultVercelAPIBaseURL = "https://api.vercel.com"
	minCertRemainingTTL     = 24 * time.Hour
)

type vercelCertsListPayload struct {
	Certs []vercelCertSummary `json:"certs"`
}

type vercelCertSummary struct {
	ID        string   `json:"id"`
	CNs       []string `json:"cns"`
	ExpiresAt int64    `json:"expiresAt"`
}

type vercelIssueCertRequest struct {
	CNs []string `json:"cns"`
}

func resolveVercelAPIToken(ctx *pulumi.Context, explicit pulumi.StringInput) pulumi.StringOutput {
	if explicit != nil {
		return pulumi.ToSecret(explicit.ToStringOutput()).(pulumi.StringOutput)
	}
	cfg := config.New(ctx, "vercel")
	if cfg.Get("apiToken") != "" {
		return cfg.RequireSecret("apiToken")
	}
	return pulumi.String("").ToStringOutput()
}

func ensureAliasCertOutput(
	ctx *pulumi.Context,
	aliasHost, teamID, apiToken pulumi.StringOutput,
	apiBaseURL string,
) pulumi.StringOutput {
	if ctx.DryRun() {
		return aliasHost
	}
	return pulumi.All(aliasHost, teamID, apiToken).ApplyTWithContext(
		context.Background(),
		func(goCtx context.Context, vs []any) (string, error) {
			host := core.NormalizeDomain(vs[0].(string))
			tid := strings.TrimSpace(vs[1].(string))
			token := strings.TrimSpace(vs[2].(string))
			if host == "" || strings.HasSuffix(host, ".vercel.app") || token == "" {
				return host, nil
			}
			if err := ensureVercelCert(goCtx, token, apiBaseURL, tid, host); err != nil {
				return "", err
			}
			return host, nil
		},
	).(pulumi.StringOutput)
}

func ensureVercelCert(ctx context.Context, token, apiBaseURL, teamID, host string) error {
	baseURL := strings.TrimRight(defaultVercelStr(apiBaseURL, defaultVercelAPIBaseURL), "/")
	client := &http.Client{Timeout: 45 * time.Second}
	covered, err := hasValidVercelCert(ctx, client, baseURL, token, teamID, host, time.Now())
	if err == nil && covered {
		return nil
	}
	return issueVercelCertWithRetry(ctx, client, baseURL, token, teamID, host)
}

func hasValidVercelCert(
	ctx context.Context,
	client *http.Client,
	baseURL, token, teamID, host string,
	now time.Time,
) (bool, error) {
	endpoint := buildVercelCertsURL(baseURL, teamID, true)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("vercel list certs returned %d", resp.StatusCode)
	}
	var payload vercelCertsListPayload
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return false, err
	}
	cutoffMillis := now.Add(minCertRemainingTTL).UnixMilli()
	for _, cert := range payload.Certs {
		if cert.ExpiresAt <= cutoffMillis {
			continue
		}
		for _, cn := range cert.CNs {
			if certCNCoversHost(cn, host) {
				return true, nil
			}
		}
	}
	return false, nil
}

func certCNCoversHost(cn, host string) bool {
	c := core.NormalizeDomain(cn)
	h := core.NormalizeDomain(host)
	if c == "" || h == "" {
		return false
	}
	if c == h {
		return true
	}
	if !strings.HasPrefix(c, "*.") {
		return false
	}
	suffix := c[1:] // ".example.com"
	if !strings.HasSuffix(h, suffix) {
		return false
	}
	label := strings.TrimSuffix(h, suffix)
	return label != "" && !strings.Contains(label, ".")
}

func issueVercelCertWithRetry(
	ctx context.Context,
	client *http.Client,
	baseURL, token, teamID, host string,
) error {
	endpoint := buildVercelCertsURL(baseURL, teamID, false)
	bodyBytes, err := json.Marshal(vercelIssueCertRequest{CNs: []string{host}})
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		retry, err := attemptIssueVercelCert(ctx, client, endpoint, token, host, bodyBytes)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	return lastErr
}

func attemptIssueVercelCert(
	ctx context.Context,
	client *http.Client,
	endpoint, token, host string,
	bodyBytes []byte,
) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return true, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return false, nil
	}
	retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return retry, fmt.Errorf("vercel issue cert for %q returned %d: %s", host, resp.StatusCode, strings.TrimSpace(string(respBody)))
}

func buildVercelCertsURL(baseURL, teamID string, withLimit bool) string {
	q := url.Values{}
	if teamID != "" {
		q.Set("teamId", teamID)
	}
	if withLimit {
		q.Set("limit", "100")
	}
	encoded := q.Encode()
	if encoded == "" {
		return baseURL + "/v7/certs"
	}
	return baseURL + "/v7/certs?" + encoded
}
