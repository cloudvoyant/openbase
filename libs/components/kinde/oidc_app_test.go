package kinde_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

// Public API -----------------------------------------------------------------

func TestKindeOidcApp(t *testing.T) {
	t.Run("given kinde.Platform, kinde.Project, and kinde.OidcApp specs", func(t *testing.T) {
		t.Run("when routing dev/preview and stage/prod stacks through Platform -> Project -> OidcApp, then each tier binds the matching tenant and callback URL", func(t *testing.T) {
			// Arrange.
			mocks := newKindeMocks()
			previewIssuerCh := make(chan string, 1)
			stageIssuerCh := make(chan string, 1)
			previewRedirectCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				plat, err := sampleKindePlatformSpec().Register(ctx)
				if err != nil {
					return err
				}
				proj, err := kinde.Project{
					Name:           "project-oidc",
					Project:        "storefront",
					Platform:       plat,
					VercelTeamSlug: "cloudvoyant",
				}.Register(ctx)
				if err != nil {
					return err
				}
				prevApp, err := kinde.OidcApp{
					Name:           "oidc-preview",
					Project:        "storefront",
					Environment:    "preview-pr-5",
					KindeProject:   proj,
					VercelTeamSlug: "cloudvoyant",
				}.Register(ctx)
				if err != nil {
					return err
				}
				stageApp, err := kinde.OidcApp{
					Name:           "oidc-stage",
					Project:        "storefront",
					Environment:    "stage",
					KindeProject:   proj,
					VercelTeamSlug: "cloudvoyant",
				}.Register(ctx)
				if err != nil {
					return err
				}
				prevApp.Link().Env["KINDE_ISSUER_URL"].ApplyT(func(v string) string { previewIssuerCh <- v; return v })
				prevApp.Link().Env["KINDE_REDIRECT_URL"].ApplyT(func(v string) string { previewRedirectCh <- v; return v })
				stageApp.Link().Env["KINDE_ISSUER_URL"].ApplyT(func(v string) string { stageIssuerCh <- v; return v })
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

		t.Run("when tenant credentials or OidcApp sources are missing, then Register rejects with sentinel errors", func(t *testing.T) {
			// Arrange / Act.
			errMissingCreds := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := kinde.Project{Project: "storefront"}.Register(ctx)
				return e
			}, pulumi.WithMocks("storefront", "shared", newKindeMocks()))

			errMissingOidcSrc := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := kinde.OidcApp{
					Name:        "oidc",
					Project:     "storefront",
					Environment: "dev",
				}.Register(ctx)
				return e
			}, pulumi.WithMocks("storefront", "dev", newKindeMocks()))

			// Assert.
			if !errors.Is(errMissingCreds, kinde.ErrTenantCredentialsRequired) {
				t.Fatalf("expected ErrTenantCredentialsRequired, got %v", errMissingCreds)
			}
			if !errors.Is(errMissingOidcSrc, kinde.ErrOidcSourceRequired) {
				t.Fatalf("expected ErrOidcSourceRequired, got %v", errMissingOidcSrc)
			}
		})

		t.Run("when resolving credentials and host parts via ProjectStackRef, then secret clientSecret and callback URL resolve cleanly", func(t *testing.T) {
			// Arrange.
			secCh := make(chan string, 1)
			redirCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				app, e := kinde.OidcApp{
					Name:            "oidc",
					Project:         "storefront",
					Environment:     "preview-pr-8",
					ProjectStackRef: "organization/storefront-shared/shared",
				}.Register(ctx)
				if e != nil {
					return e
				}
				app.Link().Secrets["KINDE_CLIENT_SECRET"].ApplyT(func(v string) string { secCh <- v; return v })
				app.Link().Env["KINDE_REDIRECT_URL"].ApplyT(func(v string) string { redirCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "preview-pr-8", newKindeMocks()))

			// Assert.
			requireKindeNoErr(t, err)
			assertKindeEq(t, "ProjectStackRef secret", <-secCh, "ref_dev_secret")
			assertKindeEq(t, "ProjectStackRef redirect", <-redirCh, "https://openbase-storefront-pr-8-cloudvoyant.vercel.app/api/auth/kinde_callback")
		})

		t.Run("when registering kinde.Project for Vercel, then dev includes default <project>.vercel.app, team, and wildcard callbacks", func(t *testing.T) {
			// Arrange.
			mocks := newKindeMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := sampleProjectSpec().Register(ctx)
				return e
			}, pulumi.WithMocks("storefront", "shared", mocks))

			// Assert.
			requireKindeNoErr(t, err)
			got := mocks.appRedirects["project-oidc-dev-app"]
			for _, want := range []string{
				"https://storefront.vercel.app/api/auth/kinde_callback",
				"https://storefront-cloudvoyant.vercel.app/api/auth/kinde_callback",
				"https://storefront-*-cloudvoyant.vercel.app/api/auth/kinde_callback",
			} {
				if !slices.Contains(got, want) {
					t.Fatalf("expected dev redirects %v to include %q", got, want)
				}
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

func sampleKindePlatformSpec() kinde.Platform {
	return kinde.Platform{
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
	}
}

func sampleProjectSpec() kinde.Project {
	return kinde.Project{
		Name:    "project-oidc",
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
	mu           sync.Mutex
	counts       map[string]int
	appRedirects map[string][]string
}

func newKindeMocks() *kindeMocks {
	return &kindeMocks{counts: map[string]int{}, appRedirects: map[string][]string{}}
}

func (m *kindeMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[a.TypeToken]++
	outs := a.Inputs.Copy()
	if a.TypeToken == "kinde:index/application:Application" {
		outs["clientId"] = resource.NewStringProperty(a.Name + "_cid")
		outs["clientSecret"] = resource.NewStringProperty(a.Name + "_csec")
		if arr, ok := a.Inputs["redirectUris"]; ok && arr.IsArray() {
			for _, item := range arr.ArrayValue() {
				m.appRedirects[a.Name] = append(m.appRedirects[a.Name], item.StringValue())
			}
		}
	}
	if a.TypeToken == "pulumi:pulumi:StackReference" {
		outs["outputs"] = resource.NewObjectProperty(resource.PropertyMap{
			"devIssuerUrl":      resource.NewStringProperty("https://dev-ref.kinde.com"),
			"devClientId":       resource.NewStringProperty("ref_dev_cid"),
			"devClientSecret":   resource.NewStringProperty("ref_dev_secret"),
			"vercelProjectName": resource.NewStringProperty("openbase-storefront"),
			"teamSlug":          resource.NewStringProperty("cloudvoyant"),
		})
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
