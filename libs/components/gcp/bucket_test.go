package gcp_test

import (
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
)

// Public API -----------------------------------------------------------------

func TestBucketProtectedProdLifecycle(t *testing.T) {
	t.Run("when creating gcp.Bucket in prod", func(t *testing.T) {
		// Arrange.
		mocks := newGCPMocks()
		var meta core.ResourceMeta

		// Act.
		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			bkt, err := gcp.NewBucket(ctx, "uploads", gcp.BucketArgs{
				StatefulResourceArgs: core.StatefulResourceArgs{
					Project:     "storefront",
					Environment: "prod",
				},
				BucketNamespace: "cv-openbase",
				CORS: []gcp.BucketCORSRule{
					{Origins: []string{"https://app.example.com"}, Methods: []string{"GET", "PUT"}},
				},
			})
			if err != nil {
				return err
			}
			meta = bkt.OpenBaseMeta()
			return nil
		}, pulumi.WithMocks("storefront", "prod", mocks))

		// Assert.
		requireGCPNoErr(t, err)
		if !meta.Protected || !meta.Backup {
			t.Fatalf("expected prod bucket Protected=true and Backup=true, got %+v", meta)
		}
		rec, ok := mocks.findFirst("gcp:storage/bucket:Bucket")
		if !ok || rec.inputs["forceDestroy"].BoolValue() {
			t.Fatalf("expected forceDestroy=false on prod bucket, got %+v", rec.inputs)
		}
	})
}

func TestBucketPreviewBranchOverlayAndGrants(t *testing.T) {
	t.Run("when branching gcp.Bucket in preview from dev and linking to consumers", func(t *testing.T) {
		// Arrange.
		mocks := newGCPMocks()
		primaryCh := make(chan string, 1)
		fallbackCh := make(chan string, 1)

		// Act.
		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			bkt, err := gcp.NewBucket(ctx, "uploads", gcp.BucketArgs{
				StatefulResourceArgs: core.StatefulResourceArgs{
					Project:     "storefront",
					Environment: "preview-pr-42",
					BranchFrom:  "dev",
				},
				BucketNamespace: "cv-openbase",
			})
			if err != nil {
				return err
			}
			desc := bkt.Link()
			desc.Env["STORAGE_BUCKET"].ApplyT(func(v string) string { primaryCh <- v; return v })
			desc.Env["STORAGE_FALLBACK_BUCKET"].ApplyT(func(v string) string { fallbackCh <- v; return v })
			return desc.GrantAccess(core.LinkGrantContext{
				Ctx:                         ctx,
				ConsumerName:                "storefront-preview-pr-42-api",
				Environment:                 "preview-pr-42",
				GCPServiceAccountEmail:      pulumi.String("api@cloudvoyant-openbase.iam.gserviceaccount.com").ToStringOutput(),
				GCPWorkloadIdentityPoolName: pulumi.String("projects/123/locations/global/workloadIdentityPools/pool").ToStringOutput(),
				VercelProjectID:             pulumi.String("prj_123").ToStringOutput(),
			})
		}, pulumi.WithMocks("storefront", "preview-pr-42", mocks))

		// Assert.
		requireGCPNoErr(t, err)
		if got := <-primaryCh; got != "cv-openbase-storefront-preview-pr-42-uploads" {
			t.Fatalf("primary bucket = %q", got)
		}
		if got := <-fallbackCh; got != "cv-openbase-storefront-dev-uploads" {
			t.Fatalf("fallback bucket = %q", got)
		}
		mocks.assertCount(t, "gcp:storage/bucketIAMMember:BucketIAMMember", 4)
	})
}
