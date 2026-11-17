package vercel_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

func TestBlobStoreStagePropagationAndPreviewOverlay(t *testing.T) {
	t.Run("when creating stage and preview BlobStores", func(t *testing.T) {
		// Arrange.
		mocks := newVercelMocks()
		prefixCh := make(chan string, 1)
		fallbackCh := make(chan string, 1)

		// Act.
		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			_, err := vercel.NewBlobStore(ctx, "uploads-stage", vercel.BlobStoreArgs{
				StatefulResourceArgs:     core.StatefulResourceArgs{Project: "storefront", Environment: "stage"},
				StoreID:                  pulumi.String("store_AbcXyz"),
				VercelProjectID:          pulumi.String("prj_1"),
				StageCustomEnvironmentID: pulumi.String("env_stage_1"),
				BlobReadWriteToken:       pulumi.String("vercel_blob_rw_test"),
			})
			if err != nil {
				return err
			}
			prev, err := vercel.NewBlobStore(ctx, "uploads-preview", vercel.BlobStoreArgs{
				StatefulResourceArgs: core.StatefulResourceArgs{
					Project:     "storefront",
					Environment: "preview-pr-9",
					BranchFrom:  "dev",
				},
				StoreID: pulumi.String("store_AbcXyz"),
			})
			if err != nil {
				return err
			}
			prev.Link().Env["BLOB_PREFIX"].ApplyT(func(v string) string { prefixCh <- v; return v })
			prev.Link().Env["BLOB_FALLBACK_PREFIX"].ApplyT(func(v string) string { fallbackCh <- v; return v })
			return nil
		}, pulumi.WithMocks("storefront", "stage", mocks))

		// Assert.
		requireVercelNoErr(t, err)
		mocks.assertCount(t, "vercel:index/projectEnvironmentVariable:ProjectEnvironmentVariable", 2)
		assertVercelEq(t, "preview prefix", <-prefixCh, "preview-pr-9/")
		assertVercelEq(t, "fallback prefix", <-fallbackCh, "dev/")
	})
}

func TestBlobOverlayClientFallbackReads(t *testing.T) {
	t.Run("when primary prefix misses and fallback prefix hits", func(t *testing.T) {
		// Arrange.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/dev/logo.txt") {
				_, _ = w.Write([]byte("dev-logo"))
				return
			}
			http.NotFound(w, r)
		}))
		defer srv.Close()
		client, err := vercel.NewBlobOverlayClient(srv.URL, "tok", "preview-pr-9", "dev")
		requireVercelNoErr(t, err)

		// Act.
		data, src, errHit := client.Get(context.Background(), "logo.txt")
		_, _, errMiss := client.Get(context.Background(), "missing.txt")

		// Assert.
		requireVercelNoErr(t, errHit)
		assertVercelEq(t, "source prefix", src, "dev/")
		assertVercelEq(t, "payload", string(data), "dev-logo")
		if !errors.Is(errMiss, vercel.ErrBlobNotFound) {
			t.Fatalf("expected ErrBlobNotFound, got %v", errMiss)
		}
	})
}
