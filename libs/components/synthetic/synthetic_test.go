package synthetic_test

import (
	"slices"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/synthetic"
)

// Public API -----------------------------------------------------------------

func TestSyntheticComponents(t *testing.T) {
	t.Run("given synthetic.Storage and synthetic.Service specs in preview-pr-12", func(t *testing.T) {
		t.Run("when registering a Service linked to a branched Storage, then it records consumer grants while caller wires explicit Env", func(t *testing.T) {
			// Arrange.
			bucketCh := make(chan string, 1)
			fallbackCh := make(chan string, 1)
			urlCh := make(chan string, 1)
			var granted []string
			var branchSrc string

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				uploads, err := synthetic.NewStorage(ctx, "uploads", &synthetic.StorageArgs{
					Project:     "storefront",
					Environment: "preview-pr-12",
					BranchFrom:  "dev",
				})
				if err != nil {
					return err
				}
				api, err := synthetic.NewService(ctx, "api", &synthetic.ServiceArgs{
					Project:     "storefront",
					Environment: "preview-pr-12",
					Links:       []core.Resource{uploads},
					Env: map[string]pulumi.StringInput{
						"PRIMARY_BUCKET":  uploads.BucketName,
						"FALLBACK_BUCKET": uploads.FallbackBucketName,
					},
				})
				if err != nil {
					return err
				}
				branchSrc = uploads.BranchSource()
				granted = uploads.GrantedConsumers
				api.ResolvedEnv["PRIMARY_BUCKET"].ApplyT(func(v string) string { bucketCh <- v; return v })
				api.ResolvedEnv["FALLBACK_BUCKET"].ApplyT(func(v string) string { fallbackCh <- v; return v })
				api.URL.ApplyT(func(v string) string { urlCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "preview-pr-12", &mockMonitor{}))

			// Assert.
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if branchSrc != "dev" {
				t.Fatalf("BranchSource() = %q, want dev", branchSrc)
			}
			if got := <-bucketCh; got != "storefront-preview-pr-12-uploads" {
				t.Fatalf("bucket = %q", got)
			}
			if got := <-fallbackCh; got != "storefront-dev-uploads" {
				t.Fatalf("fallback = %q", got)
			}
			if got := <-urlCh; got != "https://storefront-preview-pr-12-api.synthetic.local" {
				t.Fatalf("url = %q", got)
			}
			wantGrants := []string{"preview-pr-12:storefront-preview-pr-12-api"}
			if !slices.Equal(granted, wantGrants) {
				t.Fatalf("granted = %v, want %v", granted, wantGrants)
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

type mockMonitor struct{}

func (*mockMonitor) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	return a.Name + "_id", a.Inputs, nil
}

func (*mockMonitor) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) { return a.Args, nil }
