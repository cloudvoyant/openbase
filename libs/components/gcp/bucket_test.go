package gcp_test

import (
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
)

// Public API -----------------------------------------------------------------

func TestBucketLifecycleAndBranchGrants(t *testing.T) {
	t.Run("given gcp.Bucket specs across persistent and preview tiers", func(t *testing.T) {
		t.Run("when registering a prod Bucket, then it enables Protect, Backup, and forceDestroy=false", func(t *testing.T) {
			// Arrange.
			mocks := newGCPMocks()
			var meta core.ResourceMeta

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				bkt, err := gcp.NewBucket(ctx, "uploads", &gcp.BucketArgs{
					ProjectName:     "storefront",
					Environment:     "prod",
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

		t.Run("when branching a preview Bucket from dev and linking consumers, then it resolves fallback bucket outputs and binds primary/fallback IAM", func(t *testing.T) {
			// Arrange.
			mocks := newGCPMocks()
			primaryCh := make(chan string, 1)
			fallbackCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				bkt, err := gcp.NewBucket(ctx, "uploads", &gcp.BucketArgs{
					ProjectName:        "storefront",
					Environment:        "preview-pr-42",
					BranchFrom:         "dev",
					BucketNamespace:    "cv-openbase",
					FallbackBucketName: pulumi.String("cv-openbase-storefront-dev-uploads"),
				})
				if err != nil {
					return err
				}
				bkt.BucketName.ApplyT(func(v string) string { primaryCh <- v; return v })
				bkt.FallbackBucketName.ApplyT(func(v string) string { fallbackCh <- v; return v })
				if _, err := core.ResolveLinks(core.LinkGrantContext{
					Ctx:          ctx,
					ConsumerName: "storefront-preview-pr-42-api",
					Environment:  "preview-pr-42",
					Subject: core.Subject{
						Provider: "gcp",
						NativeID: pulumi.String("serviceAccount:api@cloudvoyant-openbase.iam.gserviceaccount.com").ToStringOutput(),
					},
				}, []core.Resource{bkt}); err != nil {
					return err
				}
				_, err = core.ResolveLinks(core.LinkGrantContext{
					Ctx:          ctx,
					ConsumerName: "storefront-preview-pr-42-web",
					ConsumerMeta: core.ResourceMeta{
						Name:          "web",
						CanonicalName: "storefront-preview-pr-42-web",
						Project:       "storefront",
						Environment:   "preview-pr-42",
					},
					Environment: "preview-pr-42",
					Subject: core.Subject{
						Provider: "vercel",
						Claims:   map[string]pulumi.StringOutput{"project_id": pulumi.String("prj_123").ToStringOutput()},
					},
					Brokers: []core.IdentityBroker{gcp.WIFBroker{
						GCPProject: pulumi.String("cloudvoyant-openbase"),
						PoolName:   pulumi.String("projects/123/locations/global/workloadIdentityPools/pool"),
					}},
				}, []core.Resource{bkt})
				return err
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
	})
}
