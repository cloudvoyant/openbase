package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

func TestVercelBlobStore(t *testing.T) {
	baseBackend := RequireLiveVercel(t)
	teamID, teamSlug := detectVercelTeam(t)
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "obvblob" + runID
	h := NewLiveHarness(
		t,
		"vercel-blob",
		"openbase-vercel-blob-fixture",
		baseBackend,
		"VERCEL_API_TOKEN="+os.Getenv("VERCEL_TOKEN"),
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"OPENBASE_TEST_VERCEL_TEAM_ID="+teamID,
		"OPENBASE_TEST_VERCEL_TEAM_SLUG="+teamSlug,
	)

	var vercelProjectID, stageEnvID, blobStoreID, blobBaseURL, blobRWToken string
	t.Cleanup(func() {
		purgeTestBlobs(blobBaseURL, blobRWToken)
	})

	t.Run("when applying Shared + BlobStore across dev, stage, prod, and preview", func(t *testing.T) {
		// Arrange.
		for _, s := range []string{"shared", "dev", "stage", "prod", "preview-pr-3"} {
			h.InitStack(t, s)
		}

		// Act.
		h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
		vercelProjectID = h.Output(t, "shared", "projectId")
		stageEnvID = h.Output(t, "shared", "stageCustomEnvironmentId")
		blobStoreID = h.Output(t, "shared", "blobStoreId")
		blobBaseURL = h.Output(t, "shared", "blobBaseUrl")
		blobRWToken = fetchProjectBlobRWToken(t, teamID, vercelProjectID)

		stackEnv := []string{
			"OPENBASE_TEST_BLOB_STORE_ID=" + blobStoreID,
			"OPENBASE_TEST_VERCEL_PROJECT_ID=" + vercelProjectID,
			"OPENBASE_TEST_STAGE_ENV_ID=" + stageEnvID,
			"OPENBASE_TEST_BLOB_RW_TOKEN=" + blobRWToken,
		}
		for _, s := range []string{"dev", "stage", "prod", "preview-pr-3"} {
			h.Pulumi(t, stackEnv, "up", "--stack", s, "--yes", "--non-interactive", "--skip-preview")
		}

		devPrefix := h.Output(t, "dev", "blobPrefix")
		stagePrefix := h.Output(t, "stage", "blobPrefix")
		previewPrefix := h.Output(t, "preview-pr-3", "blobPrefix")
		previewFallback := h.Output(t, "preview-pr-3", "blobFallbackPrefix")

		// Assert.
		t.Run("it uploads/reads blobs in dev and stage and propagates BLOB_READ_WRITE_TOKEN to CustomEnvironment", func(t *testing.T) {
			assertStageBlobEnvPropagated(t, teamID, vercelProjectID, stageEnvID)
			verifyDevAndStageBlobIO(t, blobBaseURL, blobRWToken, devPrefix, stagePrefix)
		})

		t.Run("it reads dev seed blobs via fallback in preview while isolating preview writes under BLOB_PREFIX", func(t *testing.T) {
			verifyPreviewBlobOverlay(t, blobBaseURL, blobRWToken, devPrefix, previewPrefix, previewFallback)
		})
	})

	t.Run("when destroying preview", func(t *testing.T) {
		// Arrange / Act.
		h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-3", "--yes", "--non-interactive", "--skip-preview")
		h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-3", "--yes", "--non-interactive", "--skip-preview")

		// Assert.
		t.Run("it cleans up preview blob state idempotently while keeping dev/stage blobs intact", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			devClient, err := vercel.NewBlobOverlayClient(blobBaseURL, blobRWToken, "dev/", "")
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := devClient.Get(ctx, "seed.txt")
			if err != nil {
				t.Fatalf("dev seed read after preview destroy: %v", err)
			}
			assertEqualStr(t, "dev seed intact", string(got), "dev-blob-seed")
		})
	})

	h.OnCleanupVerify(func(t *testing.T) {
		if vercelProjectID != "" {
			assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v9/projects/%s?teamId=%s", vercelProjectID, teamID), http.StatusNotFound)
		}
		if blobStoreID != "" {
			assertVercelStatus(t, fmt.Sprintf("https://api.vercel.com/v1/storage/stores/%s?teamId=%s", blobStoreID, teamID), http.StatusNotFound)
		}
	})
}

// Private helpers ------------------------------------------------------------

func purgeTestBlobs(baseURL, rwToken string) {
	if baseURL == "" || rwToken == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := vercel.NewBlobOverlayClient(baseURL, rwToken, "", "")
	if err != nil {
		return
	}
	_ = c.Delete(ctx, "dev/seed.txt", "stage/stage.txt", "preview-pr-3/delta.txt")
}

func detectVercelTeam(t *testing.T) (string, string) {
	t.Helper()
	raw := vercelGET(t, "https://api.vercel.com/v2/teams", http.StatusOK)
	var parsed struct {
		Teams []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Teams) == 0 {
		t.Fatalf("failed to resolve Vercel team: %v", err)
	}
	return parsed.Teams[0].ID, parsed.Teams[0].Slug
}

func fetchProjectBlobRWToken(t *testing.T, teamID, projectID string) string {
	t.Helper()
	raw := vercelGET(t, fmt.Sprintf("https://api.vercel.com/v10/projects/%s/env?teamId=%s&decrypt=true", projectID, teamID), http.StatusOK)
	var list struct {
		Envs []struct {
			ID     string   `json:"id"`
			Key    string   `json:"key"`
			Value  string   `json:"value"`
			Target []string `json:"target"`
		} `json:"envs"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	for _, ev := range list.Envs {
		if ev.Key != "BLOB_READ_WRITE_TOKEN" || !hasString(ev.Target, "development") {
			continue
		}
		if strings.HasPrefix(ev.Value, "vercel_blob_rw_") {
			return ev.Value
		}
		single := vercelGET(t, fmt.Sprintf("https://api.vercel.com/v9/projects/%s/env/%s?teamId=%s", projectID, ev.ID, teamID), http.StatusOK)
		var detail struct {
			Value string `json:"value"`
		}
		_ = json.Unmarshal(single, &detail)
		if strings.HasPrefix(detail.Value, "vercel_blob_rw_") {
			return detail.Value
		}
	}
	t.Fatalf("BLOB_READ_WRITE_TOKEN not found on project %s", projectID)
	return ""
}

func assertStageBlobEnvPropagated(t *testing.T, teamID, projectID, stageEnvID string) {
	t.Helper()
	raw := vercelGET(t, fmt.Sprintf("https://api.vercel.com/v10/projects/%s/env?teamId=%s", projectID, teamID), http.StatusOK)
	var list struct {
		Envs []struct {
			Key                  string   `json:"key"`
			CustomEnvironmentIDs []string `json:"customEnvironmentIds"`
		} `json:"envs"`
	}
	_ = json.Unmarshal(raw, &list)
	var hasToken, hasBase bool
	for _, ev := range list.Envs {
		if !hasString(ev.CustomEnvironmentIDs, stageEnvID) {
			continue
		}
		if ev.Key == "BLOB_READ_WRITE_TOKEN" {
			hasToken = true
		}
		if ev.Key == "BLOB_BASE_URL" {
			hasBase = true
		}
	}
	if !hasToken || !hasBase {
		t.Fatalf("expected BLOB_READ_WRITE_TOKEN (%v) and BLOB_BASE_URL (%v) in stage custom env %s", hasToken, hasBase, stageEnvID)
	}
}

func verifyDevAndStageBlobIO(t *testing.T, baseURL, rwToken, devPrefix, stagePrefix string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	devClient, err := vercel.NewBlobOverlayClient(baseURL, rwToken, devPrefix, "")
	if err != nil {
		t.Fatal(err)
	}
	stageClient, err := vercel.NewBlobOverlayClient(baseURL, rwToken, stagePrefix, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := devClient.Put(ctx, "seed.txt", []byte("dev-blob-seed"), "text/plain"); err != nil {
		t.Fatalf("dev blob put: %v", err)
	}
	if _, err := stageClient.Put(ctx, "stage.txt", []byte("stage-blob-ok"), "text/plain"); err != nil {
		t.Fatalf("stage blob put: %v", err)
	}
	stageBytes, _, err := stageClient.Get(ctx, "stage.txt")
	if err != nil {
		t.Fatalf("stage blob get: %v", err)
	}
	assertEqualStr(t, "stage blob content", string(stageBytes), "stage-blob-ok")
}

func verifyPreviewBlobOverlay(t *testing.T, baseURL, rwToken, devPrefix, previewPrefix, previewFallback string) {
	t.Helper()
	assertEqualStr(t, "preview fallback prefix", previewFallback, devPrefix)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	devClient, _ := vercel.NewBlobOverlayClient(baseURL, rwToken, devPrefix, "")
	previewClient, _ := vercel.NewBlobOverlayClient(baseURL, rwToken, previewPrefix, previewFallback)

	seedBytes, src, err := previewClient.Get(ctx, "seed.txt")
	if err != nil {
		t.Fatalf("preview fallback read: %v", err)
	}
	assertEqualStr(t, "fallback source prefix", src, devPrefix)
	assertEqualStr(t, "fallback seed bytes", string(seedBytes), "dev-blob-seed")

	if _, err := previewClient.Put(ctx, "delta.txt", []byte("only-in-preview"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := devClient.Get(ctx, "delta.txt"); !errors.Is(err, vercel.ErrBlobNotFound) {
		t.Fatalf("expected delta.txt absent from dev prefix, got err=%v", err)
	}
}

func vercelGET(t *testing.T, url string, wantStatus int) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("VERCEL_TOKEN"))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Vercel GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("Vercel GET %s status = %d, want %d: %s", url, resp.StatusCode, wantStatus, string(raw))
	}
	return raw
}

func assertVercelStatus(t *testing.T, url string, want int) {
	t.Helper()
	_ = vercelGET(t, url, want)
}

func hasString(items []string, needle string) bool {
	for _, s := range items {
		if s == needle {
			return true
		}
	}
	return false
}
