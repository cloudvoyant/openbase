package vercel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Public API -----------------------------------------------------------------

// ErrBlobNotFound reports that a blob object does not exist in primary or fallback prefixes.
var ErrBlobNotFound = errors.New("vercel: blob object not found")

// BlobOverlayClient performs prefix-isolated Vercel Blob reads/writes with BranchFrom fallback.
type BlobOverlayClient struct {
	BaseURL        string
	ReadWriteToken string
	PrimaryPrefix  string
	FallbackPrefix string
	HTTPClient     *http.Client
}

// NewBlobOverlayClient constructs a Vercel Blob overlay client.
func NewBlobOverlayClient(baseURL, rwToken, primaryPrefix, fallbackPrefix string) (*BlobOverlayClient, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("vercel: blob base URL is required")
	}
	return &BlobOverlayClient{
		BaseURL:        strings.TrimRight(baseURL, "/"),
		ReadWriteToken: strings.TrimSpace(rwToken),
		PrimaryPrefix:  ensureTrailingSlash(primaryPrefix),
		FallbackPrefix: ensureTrailingSlash(fallbackPrefix),
		HTTPClient:     &http.Client{Timeout: 20 * time.Second},
	}, nil
}

// Put writes data under PrimaryPrefix + key using deterministic pathname overwrite.
func (c *BlobOverlayClient) Put(ctx context.Context, key string, data []byte, contentType string) (string, error) {
	pathname := c.PrimaryPrefix + strings.TrimLeft(key, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "https://blob.vercel-storage.com/"+pathname, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.ReadWriteToken)
	req.Header.Set("x-api-version", "7")
	req.Header.Set("x-add-random-suffix", "0")
	req.Header.Set("x-allow-overwrite", "1")
	if contentType != "" {
		req.Header.Set("x-content-type", contentType)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("vercel blob put %s: status %d: %s", pathname, resp.StatusCode, string(raw))
	}
	var parsed struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	return parsed.URL, nil
}

// Get reads PrimaryPrefix + key first and falls back to FallbackPrefix + key on 404.
func (c *BlobOverlayClient) Get(ctx context.Context, key string) ([]byte, string, error) {
	cleanKey := strings.TrimLeft(key, "/")
	primaryPath := c.PrimaryPrefix + cleanKey
	data, err := c.fetchPublicOrAuth(ctx, primaryPath)
	if err == nil {
		return data, c.PrimaryPrefix, nil
	}
	if !errors.Is(err, ErrBlobNotFound) || c.FallbackPrefix == "" {
		return nil, "", err
	}
	fallbackData, fallbackErr := c.fetchPublicOrAuth(ctx, c.FallbackPrefix+cleanKey)
	if fallbackErr != nil {
		return nil, "", fallbackErr
	}
	return fallbackData, c.FallbackPrefix, nil
}

// Delete removes one or more full blob URLs or PrimaryPrefix keys from the store.
func (c *BlobOverlayClient) Delete(ctx context.Context, keys ...string) error {
	urls := make([]string, 0, len(keys))
	for _, k := range keys {
		if strings.HasPrefix(k, "https://") {
			urls = append(urls, k)
		} else {
			urls = append(urls, c.BaseURL+"/"+c.PrimaryPrefix+strings.TrimLeft(k, "/"))
		}
	}
	payload, _ := json.Marshal(map[string][]string{"urls": urls})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://blob.vercel-storage.com/delete", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.ReadWriteToken)
	req.Header.Set("x-api-version", "7")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("vercel blob delete: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// Private helpers ------------------------------------------------------------

func (c *BlobOverlayClient) fetchPublicOrAuth(ctx context.Context, pathname string) ([]byte, error) {
	target := c.BaseURL + "/" + strings.TrimLeft(pathname, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if c.ReadWriteToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.ReadWriteToken)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrBlobNotFound
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vercel blob get %s: status %d: %s", target, resp.StatusCode, string(raw))
	}
	return raw, nil
}

func (c *BlobOverlayClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func ensureTrailingSlash(prefix string) string {
	trimmed := strings.Trim(prefix, "/")
	if trimmed == "" {
		return ""
	}
	return trimmed + "/"
}
