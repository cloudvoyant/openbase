package vercel_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

func TestBlobStoreAndOverlayClient(t *testing.T) {
	t.Run("given vercel.BlobStore specs and BlobOverlayClient", func(t *testing.T) {
		t.Run("when registering stage and preview BlobStores, then stage propagates CustomEnvironment vars and preview computes fallback prefixes", func(t *testing.T) {
			// Arrange.
			mocks := newVercelMocks()
			var gotPrefix, gotFallback string

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := vercel.NewBlobStore(ctx, "uploads-stage", &vercel.BlobStoreArgs{
					Project:                  "storefront",
					Environment:              "stage",
					StoreID:                  pulumi.String("store_AbcXyz"),
					VercelProjectID:          pulumi.String("prj_1"),
					StageCustomEnvironmentID: pulumi.String("env_stage_1"),
					BlobReadWriteToken:       pulumi.String("vercel_blob_rw_test"),
				})
				if err != nil {
					return err
				}
				prev, err := vercel.NewBlobStore(ctx, "uploads-preview", &vercel.BlobStoreArgs{
					Project:     "storefront",
					Environment: "preview-pr-9",
					BranchFrom:  "dev",
					StoreID:     pulumi.String("store_AbcXyz"),
				})
				if err != nil {
					return err
				}
				gotPrefix = prev.BlobPrefix
				gotFallback = prev.BlobFallbackPrefix
				return nil
			}, pulumi.WithMocks("storefront", "stage", mocks))

			// Assert.
			requireVercelNoErr(t, err)
			mocks.assertCount(t, "vercel:index/projectEnvironmentVariable:ProjectEnvironmentVariable", 2)
			assertVercelEq(t, "preview prefix", gotPrefix, "preview-pr-9/")
			assertVercelEq(t, "fallback prefix", gotFallback, "dev/")
		})

		t.Run("when primary prefix misses on BlobOverlayClient.Get, then it falls back to FallbackPrefix or returns ErrBlobNotFound", func(t *testing.T) {
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
	})
}
