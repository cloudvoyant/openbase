package gcp_test

import (
	"errors"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
)

// Public API -----------------------------------------------------------------

func TestCloudRunService(t *testing.T) {
	t.Run("given gcp.CloudRunService specs", func(t *testing.T) {
		t.Run("when deploying a public service on a long preview branch, then SA and service names stay bounded and secrets mount via Secret Manager", func(t *testing.T) {
			// Arrange.
			mocks := newGCPMocks()
			var saID string

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				svc, err := gcp.CloudRunService{
					Name:                 "orders-api-service",
					Project:              "storefront",
					Environment:          "preview-feature-very-long-branch-slug-with-ticket-98765",
					Secrets:              map[string]pulumi.StringInput{"API_SECRET": pulumi.String("top-secret")},
					ArtifactRegistryRepo: pulumi.String("storefront-shared"),
					GCPProjectNumber:     pulumi.String("785485436615"),
					Image:                pulumi.String("us-docker.pkg.dev/cloudrun/container/hello"),
					Visibility:           gcp.VisibilityPublic,
					StartupProbe:         &gcp.ProbeConfig{Path: "/healthz"},
					LivenessProbe:        &gcp.ProbeConfig{Path: "/livez"},
				}.Register(ctx)
				if err != nil {
					return err
				}
				saID = svc.ServiceAccountID
				return nil
			}, pulumi.WithMocks("storefront", "preview-feature-very-long-branch-slug-with-ticket-98765", mocks))

			// Assert.
			requireGCPNoErr(t, err)
			assertBoundedSAAndServiceName(t, mocks, saID)
			mocks.assertCount(t, "gcp:serviceaccount/account:Account", 1)
			mocks.assertCount(t, "gcp:serviceaccount/iAMMember:IAMMember", 1)
			mocks.assertCount(t, "gcp:artifactregistry/repositoryIamMember:RepositoryIamMember", 2)
			mocks.assertCount(t, "gcp:secretmanager/secret:Secret", 1)
			mocks.assertCount(t, "gcp:secretmanager/secretVersion:SecretVersion", 1)
			mocks.assertCount(t, "gcp:secretmanager/secretIamMember:SecretIamMember", 1)
			mocks.assertCount(t, "gcp:cloudrunv2/serviceIamMember:ServiceIamMember", 1)
		})

		t.Run("when linking a caller service to an internal service with DomainMapping, then it exports ORDERS_URL and binds roles/run.invoker", func(t *testing.T) {
			// Arrange.
			mocks := newGCPMocks()
			urlCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				backend, err := gcp.CloudRunService{
					Name:        "orders",
					Project:     "storefront",
					Environment: "prod",
					Image:       pulumi.String("us-docker.pkg.dev/cloudrun/container/hello"),
					Visibility:  gcp.VisibilityInternal,
					Domain:      "orders.example.com",
				}.Register(ctx)
				if err != nil {
					return err
				}
				caller, err := gcp.CloudRunService{
					Name:        "gateway",
					Project:     "storefront",
					Environment: "prod",
					Links:       []core.Resource{backend},
					Image:       pulumi.String("us-docker.pkg.dev/cloudrun/container/hello"),
					Visibility:  gcp.VisibilityWorker,
				}.Register(ctx)
				if err != nil {
					return err
				}
				backend.Link().Env["ORDERS_URL"].ApplyT(func(v string) string { urlCh <- v; return v })
				_ = caller
				return nil
			}, pulumi.WithMocks("storefront", "prod", mocks))

			// Assert.
			requireGCPNoErr(t, err)
			if got := <-urlCh; got != "https://orders-svc.a.run.app" {
				t.Fatalf("ORDERS_URL = %q", got)
			}
			mocks.assertCount(t, "gcp:cloudrun/domainMapping:DomainMapping", 1)
			mocks.assertCount(t, "gcp:cloudrunv2/serviceIamMember:ServiceIamMember", 1)
		})

		t.Run("when Image is missing or Visibility is invalid, then Register rejects with sentinel errors", func(t *testing.T) {
			// Arrange / Act.
			errMissingImage := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := gcp.CloudRunService{
					Name:        "api",
					Project:     "storefront",
					Environment: "dev",
				}.Register(ctx)
				return err
			}, pulumi.WithMocks("storefront", "dev", newGCPMocks()))

			errBadVisibility := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := gcp.CloudRunService{
					Name:        "api",
					Project:     "storefront",
					Environment: "dev",
					Image:       pulumi.String("us-docker.pkg.dev/cloudrun/container/hello"),
					Visibility:  "dmz",
				}.Register(ctx)
				return err
			}, pulumi.WithMocks("storefront", "dev", newGCPMocks()))

			// Assert.
			if !errors.Is(errMissingImage, gcp.ErrImageRequired) {
				t.Fatalf("expected ErrImageRequired, got %v", errMissingImage)
			}
			if !errors.Is(errBadVisibility, gcp.ErrInvalidVisibility) {
				t.Fatalf("expected ErrInvalidVisibility, got %v", errBadVisibility)
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

func assertBoundedSAAndServiceName(t *testing.T, mocks *gcpMocks, saID string) {
	t.Helper()
	if len(saID) < 6 || len(saID) > 30 {
		t.Fatalf("ServiceAccountID %q length %d outside [6,30]", saID, len(saID))
	}
	svcRec, ok := mocks.findFirst("gcp:cloudrunv2/service:Service")
	if !ok || len(svcRec.inputs["name"].StringValue()) > 49 {
		t.Fatalf("Cloud Run service name must be <= 49 chars, got %+v", svcRec.inputs["name"])
	}
}
