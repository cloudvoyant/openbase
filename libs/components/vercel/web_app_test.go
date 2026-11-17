package vercel_test

import (
	"errors"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

func TestVercelWebApp(t *testing.T) {
	t.Run("given vercel.WebApp specs", func(t *testing.T) {
		t.Run("when deploying a preview WebApp with GCP Workload Identity Federation, then it creates a federated SA, Deployment, and squat-proof Alias", func(t *testing.T) {
			// Arrange.
			mocks := newVercelMocks()
			urlCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				app, err := vercel.WebApp{
					Name:                    "web",
					Project:                 "storefront",
					Environment:             "preview-pr-22",
					Secrets:                 map[string]pulumi.StringInput{"SESSION_SECRET": pulumi.String("s3cr3t")},
					VercelProjectID:         pulumi.String("prj_123"),
					VercelProjectName:       "openbase-storefront",
					TeamSlug:                "cloudvoyant",
					SourceDir:               ".",
					GCPProject:              pulumi.String("cloudvoyant-openbase"),
					GCPWorkloadIdentityPool: pulumi.String("projects/123/locations/global/workloadIdentityPools/pool"),
					GCPVercelOIDCProvider:   pulumi.String("projects/123/locations/global/workloadIdentityPools/pool/providers/vercel-oidc"),
				}.Register(ctx)
				if err != nil {
					return err
				}
				app.URL.ApplyT(func(v string) string { urlCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "preview-pr-22", mocks))

			// Assert.
			requireVercelNoErr(t, err)
			assertVercelEq(t, "preview alias URL", <-urlCh, "https://openbase-storefront-pr-22-cloudvoyant.vercel.app")
			mocks.assertCount(t, "gcp:serviceaccount/account:Account", 1)
			mocks.assertCount(t, "gcp:serviceaccount/iAMMember:IAMMember", 1)
			mocks.assertCount(t, "vercel:index/deployment:Deployment", 1)
			mocks.assertCount(t, "vercel:index/alias:Alias", 1)
		})

		t.Run("when resolving projectName and teamSlug from SharedStackRef, then stage computes the team-scoped alias URL", func(t *testing.T) {
			// Arrange.
			urlCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				app, err := vercel.WebApp{
					Name:           "web",
					Project:        "storefront",
					Environment:    "stage",
					SharedStackRef: "org/storefront/shared",
				}.Register(ctx)
				if err != nil {
					return err
				}
				app.URL.ApplyT(func(v string) string { urlCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "stage", newVercelMocks()))

			// Assert.
			requireVercelNoErr(t, err)
			assertVercelEq(t, "stage alias URL from StackReference", <-urlCh, "https://openbase-storefront-stage-cloudvoyant.vercel.app")
		})

		t.Run("when Shared, SharedStackRef, and VercelProjectID are all omitted, then Register returns ErrProjectSourceRequired", func(t *testing.T) {
			// Arrange / Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := vercel.WebApp{
					Name:        "web",
					Project:     "storefront",
					Environment: "dev",
				}.Register(ctx)
				return e
			}, pulumi.WithMocks("storefront", "dev", newVercelMocks()))

			// Assert.
			if !errors.Is(err, vercel.ErrProjectSourceRequired) {
				t.Fatalf("expected ErrProjectSourceRequired, got %v", err)
			}
		})
	})
}
