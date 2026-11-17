package kinde_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

// Public API -----------------------------------------------------------------

func TestSharedAuthAndEnvironmentRouting(t *testing.T) {
	t.Run("when routing dev/preview to dev tenant and stage/prod to prod tenant", func(t *testing.T) {
		// Arrange.
		mocks := newKindeMocks()
		previewIssuerCh := make(chan string, 1)
		stageIssuerCh := make(chan string, 1)
		previewRedirectCh := make(chan string, 1)

		// Act.
		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			shared, err := kinde.NewSharedAuth(ctx, "shared-auth", sampleSharedArgs())
			if err != nil {
				return err
			}
			prevAuth, err := kinde.NewAuth(ctx, "auth-preview", kinde.AuthArgs{
				StatelessResourceArgs: core.StatelessResourceArgs{Project: "storefront", Environment: "preview-pr-5"},
				Shared:                shared,
				VercelTeamSlug:        "cloudvoyant",
			})
			if err != nil {
				return err
			}
			stageAuth, err := kinde.NewAuth(ctx, "auth-stage", kinde.AuthArgs{
				StatelessResourceArgs: core.StatelessResourceArgs{Project: "storefront", Environment: "stage"},
				Shared:                shared,
				VercelTeamSlug:        "cloudvoyant",
			})
			if err != nil {
				return err
			}
			prevAuth.Link().Env["KINDE_ISSUER_URL"].ApplyT(func(v string) string { previewIssuerCh <- v; return v })
			prevAuth.Link().Env["KINDE_REDIRECT_URL"].ApplyT(func(v string) string { previewRedirectCh <- v; return v })
			stageAuth.Link().Env["KINDE_ISSUER_URL"].ApplyT(func(v string) string { stageIssuerCh <- v; return v })
			return nil
		}, pulumi.WithMocks("storefront", "shared", mocks))

		// Assert.
		requireKindeNoErr(t, err)
		mocks.assertCount(t, "pulumi:providers:kinde", 2)
		mocks.assertCount(t, "kinde:index/application:Application", 2)
		mocks.assertCount(t, "kinde:index/applicationConnection:ApplicationConnection", 1)
		assertKindeEq(t, "preview issuer", <-previewIssuerCh, "https://dev-test.kinde.com")
		assertKindeEq(t, "stage issuer", <-stageIssuerCh, "https://prod-test.kinde.com")
		assertKindeEq(t, "preview redirect", <-previewRedirectCh, "https://storefront-pr-5-cloudvoyant.vercel.app/api/auth/kinde_callback")
	})
}

func TestKindeValidationErrors(t *testing.T) {
	t.Run("when tenant credentials or Auth sources are missing", func(t *testing.T) {
		// Arrange / Act.
		errMissingCreds := pulumi.RunErr(func(ctx *pulumi.Context) error {
			_, e := kinde.NewSharedAuth(ctx, "shared", kinde.SharedAuthArgs{Project: "storefront"})
			return e
		}, pulumi.WithMocks("storefront", "shared", newKindeMocks()))

		errMissingAuthSrc := pulumi.RunErr(func(ctx *pulumi.Context) error {
			_, e := kinde.NewAuth(ctx, "auth", kinde.AuthArgs{
				StatelessResourceArgs: core.StatelessResourceArgs{Project: "storefront", Environment: "dev"},
			})
			return e
		}, pulumi.WithMocks("storefront", "dev", newKindeMocks()))

		// Assert.
		if !errors.Is(errMissingCreds, kinde.ErrTenantCredentialsRequired) {
			t.Fatalf("expected ErrTenantCredentialsRequired, got %v", errMissingCreds)
		}
		if !errors.Is(errMissingAuthSrc, kinde.ErrAuthSourceRequired) {
			t.Fatalf("expected ErrAuthSourceRequired, got %v", errMissingAuthSrc)
		}
	})
}

// Private helpers ------------------------------------------------------------

func sampleSharedArgs() kinde.SharedAuthArgs {
	return kinde.SharedAuthArgs{
		Project: "storefront",
		Dev: kinde.TenantCredentials{
			Domain:       "dev-test.kinde.com",
			ClientID:     pulumi.String("dev-m2m-id"),
			ClientSecret: pulumi.String("dev-m2m-secret"),
		},
		Prod: kinde.TenantCredentials{
			Domain:       "https://prod-test.kinde.com/",
			ClientID:     pulumi.String("prod-m2m-id"),
			ClientSecret: pulumi.String("prod-m2m-secret"),
		},
		VercelTeamSlug: "cloudvoyant",
	}
}

type kindeMocks struct {
	mu     sync.Mutex
	counts map[string]int
}

func newKindeMocks() *kindeMocks {
	return &kindeMocks{counts: map[string]int{}}
}

func (m *kindeMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[a.TypeToken]++
	outs := a.Inputs.Copy()
	if a.TypeToken == "kinde:index/application:Application" {
		outs["clientId"] = resource.NewStringProperty(a.Name + "_cid")
		outs["clientSecret"] = resource.NewStringProperty(a.Name + "_csec")
	}
	return a.Name + "_id", outs, nil
}

func (*kindeMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) {
	if a.Token == "kinde:index/getConnections:getConnections" {
		return resource.PropertyMap{
			"connections": resource.NewArrayProperty([]resource.PropertyValue{
				resource.NewObjectProperty(resource.PropertyMap{
					"id":          resource.NewStringProperty("conn_email_pw_1"),
					"name":        resource.NewStringProperty("Email + Password"),
					"displayName": resource.NewStringProperty("Email + Password"),
					"strategy":    resource.NewStringProperty("email:password"),
				}),
			}),
		}, nil
	}
	return a.Args, nil
}

func (m *kindeMocks) assertCount(t *testing.T, typeToken string, want int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if got := m.counts[typeToken]; got != want {
		t.Fatalf("resource count for %s = %d, want %d", typeToken, got, want)
	}
}

func requireKindeNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertKindeEq(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}
