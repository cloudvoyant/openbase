package vercel_test

import (
	"errors"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

func TestVercelWebApp(t *testing.T) {
	t.Run("given vercel.WebApp specs", func(t *testing.T) {
		t.Run("when deploying a preview WebApp with GCP WIFBroker, then it creates a federated SA, Deployment, and squat-proof Alias", func(t *testing.T) {
			// Arrange.
			mocks := newVercelMocks()
			urlCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				app, err := vercel.WebApp{
					Name:              "web",
					Project:           "storefront",
					Environment:       "preview-pr-22",
					Secrets:           map[string]pulumi.StringInput{"SESSION_SECRET": pulumi.String("s3cr3t")},
					VercelProjectID:   pulumi.String("prj_123"),
					VercelProjectName: "openbase-storefront",
					TeamSlug:          "cloudvoyant",
					SourceDir:         ".",
					Brokers: []core.IdentityBroker{
						gcp.WIFBroker{
							GCPProject:   pulumi.String("cloudvoyant-openbase"),
							PoolName:     pulumi.String("projects/123/locations/global/workloadIdentityPools/pool"),
							OIDCProvider: pulumi.String("projects/123/locations/global/workloadIdentityPools/pool/providers/vercel-oidc"),
						},
					},
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

		t.Run("when resolving projectName and teamSlug from ProjectStackRef, then stage computes the team-scoped alias URL", func(t *testing.T) {
			// Arrange.
			urlCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				app, err := vercel.WebApp{
					Name:            "web",
					Project:         "storefront",
					Environment:     "stage",
					ProjectStackRef: "org/storefront-shared/shared",
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

		t.Run("when VercelProject, ProjectStackRef, and VercelProjectID are all omitted, then Register returns ErrProjectSourceRequired", func(t *testing.T) {
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

		t.Run("when linking BlobStore into a dev WebApp with explicit env vars, then it skips duplicate BLOB_READ_WRITE_TOKEN ProjectEnvironmentVariable while keeping caller env keys", func(t *testing.T) {
			// Arrange.
			mocks := newVercelMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				blobs, err := vercel.BlobStore{
					Name:               "uploads",
					Project:            "storefront",
					Environment:        "dev",
					ProjectStackRef:    "org/storefront-shared/shared",
					BlobReadWriteToken: pulumi.String("vercel_blob_rw_tok"),
				}.Register(ctx)
				if err != nil {
					return err
				}
				_, err = vercel.WebApp{
					Name:        "web",
					Project:     "storefront",
					Environment: "dev",
					Links:       []core.Resource{blobs},
					Env: map[string]pulumi.StringInput{
						"BLOB_BASE_URL": blobs.BlobBaseURL,
						"BLOB_PREFIX":   pulumi.String(blobs.BlobPrefix),
					},
					Secrets: map[string]pulumi.StringInput{
						"BLOB_READ_WRITE_TOKEN": pulumi.String("vercel_blob_rw_tok"),
					},
					ProjectStackRef: "org/storefront-shared/shared",
				}.Register(ctx)
				return err
			}, pulumi.WithMocks("storefront", "dev", mocks))

			// Assert.
			requireVercelNoErr(t, err)
			mocks.assertCount(t, "vercel:index/projectEnvironmentVariable:ProjectEnvironmentVariable", 2)
		})

		t.Run("when deploying preview with BaseDomain and ExtraDomains, then it binds {project}-{previewId}.{baseDomain} and extra aliases without registering Production ProjectDomains", func(t *testing.T) {
			// Arrange.
			mocks := newVercelMocks()
			urlCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				app, err := vercel.WebApp{
					Name:            "web",
					Project:         "storefront",
					Environment:     "preview-pr-22",
					VercelProjectID: pulumi.String("prj_123"),
					BaseDomain:      "vercel.cloudvoyant.io",
					ExtraDomains:    []string{"pr22.storefront.io"},
					SourceDir:       ".",
				}.Register(ctx)
				if err != nil {
					return err
				}
				app.URL.ApplyT(func(v string) string { urlCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "preview-pr-22", mocks))

			// Assert.
			requireVercelNoErr(t, err)
			assertVercelEq(t, "preview baseDomain URL", <-urlCh, "https://storefront-pr-22.vercel.cloudvoyant.io")
			mocks.assertCount(t, "vercel:index/projectDomain:ProjectDomain", 0)
			mocks.assertCount(t, "vercel:index/alias:Alias", 2)
		})

		t.Run("when deploying stage with SkipStageEnvironment, then it skips conflicting preview ProjectEnvironmentVariables and deploys via DeploymentArgs.Environment", func(t *testing.T) {
			// Arrange.
			mocks := newVercelMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := vercel.WebApp{
					Name:                 "web",
					Project:              "storefront",
					Environment:          "stage",
					Env:                  map[string]pulumi.StringInput{"KINDE_ISSUER_URL": pulumi.String("https://prod.kinde.com")},
					ProjectStackRef:      "org/storefront-shared/shared",
					SkipStageEnvironment: true,
					SourceDir:            ".",
				}.Register(ctx)
				return err
			}, pulumi.WithMocks("storefront", "stage", mocks))

			// Assert.
			requireVercelNoErr(t, err)
			mocks.assertCount(t, "vercel:index/projectEnvironmentVariable:ProjectEnvironmentVariable", 0)
			mocks.assertCount(t, "vercel:index/deployment:Deployment", 1)
			mocks.assertCount(t, "vercel:index/alias:Alias", 1)
		})
	})
}
