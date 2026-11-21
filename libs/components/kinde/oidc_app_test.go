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
	t.Run("given separate kinde.Platform (platform), kinde.Project (shared), and kinde.OidcApp (env) stacks", func(t *testing.T) {
		t.Run("when Platform runs on platform, Project runs on shared, and OidcApps run on preview and stage, then callbacks and NonProd/Prod tenant routing resolve across stacks", func(t *testing.T) {
			// Arrange.
			platMocks := newKindeMocks()
			sharedMocks := newKindeMocks()
			previewMocks := newKindeMocks()
			stageMocks := newKindeMocks()
			previewIssuerCh := make(chan string, 1)
			previewSecretCh := make(chan string, 1)
			previewRedirectCh := make(chan string, 1)
			stageIssuerCh := make(chan string, 1)

			// Act.
			errPlat := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := kinde.NewPlatform(ctx, "kinde-platform", sampleKindePlatformArgs())
				return err
			}, pulumi.WithMocks("platform", "platform", platMocks))

			errShared := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := kinde.NewProject(ctx, "project-oidc", &kinde.ProjectArgs{
					Project:          "storefront",
					PlatformStackRef: "organization/platform/platform",
					VercelTeamSlug:   "cloudvoyant",
				})
				return err
			}, pulumi.WithMocks("storefront-shared", "shared", sharedMocks))

			errPreview := pulumi.RunErr(func(ctx *pulumi.Context) error {
				prevApp, err := kinde.NewOidcApp(ctx, "oidc", &kinde.OidcAppArgs{
					Project:         "storefront",
					Environment:     "preview-pr-5",
					ProjectStackRef: "organization/storefront-shared/shared",
				})
				if err != nil {
					return err
				}
				prevApp.IssuerURL.ApplyT(func(v string) string { previewIssuerCh <- v; return v })
				prevApp.ClientSecret.ApplyT(func(v string) string { previewSecretCh <- v; return v })
				prevApp.RedirectURL.ApplyT(func(v string) string { previewRedirectCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "preview-pr-5", previewMocks))

			errStage := pulumi.RunErr(func(ctx *pulumi.Context) error {
				stageApp, err := kinde.NewOidcApp(ctx, "oidc", &kinde.OidcAppArgs{
					Project:         "storefront",
					Environment:     "stage",
					ProjectStackRef: "organization/storefront-shared/shared",
				})
				if err != nil {
					return err
				}
				stageApp.IssuerURL.ApplyT(func(v string) string { stageIssuerCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "stage", stageMocks))

			// Assert.
			requireKindeNoErr(t, errPlat)
			requireKindeNoErr(t, errShared)
			requireKindeNoErr(t, errPreview)
			requireKindeNoErr(t, errStage)
			platMocks.assertCount(t, "pulumi:providers:kinde", 2)
			sharedMocks.assertCount(t, "kinde:index/application:Application", 2)
			sharedMocks.assertCount(t, "kinde:index/applicationConnection:ApplicationConnection", 1)
			assertKindeEq(t, "preview issuer", <-previewIssuerCh, "https://nonprod-ref.kinde.com")
			assertKindeEq(t, "preview secret", <-previewSecretCh, "ref_nonprod_secret")
			assertKindeEq(t, "stage issuer", <-stageIssuerCh, "https://prod-ref.kinde.com")
			assertKindeEq(t, "preview redirect", <-previewRedirectCh, "https://openbase-storefront-pr-5-cloudvoyant.vercel.app/api/auth/kinde_callback")
			got := sharedMocks.appRedirects["project-oidc-nonprod-app"]
			for _, want := range []string{
				"https://openbase-storefront.vercel.app/api/auth/kinde_callback",
				"https://openbase-storefront-cloudvoyant.vercel.app/api/auth/kinde_callback",
				"https://openbase-storefront-*-cloudvoyant.vercel.app/api/auth/kinde_callback",
			} {
				if !slices.Contains(got, want) {
					t.Fatalf("expected nonProd redirects %v to include %q", got, want)
				}
			}
		})

		t.Run("when tenant credentials or OidcApp sources are missing, then NewProject and NewOidcApp reject with sentinel errors", func(t *testing.T) {
			// Arrange / Act.
			errMissingCreds := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := kinde.NewProject(ctx, "kinde", &kinde.ProjectArgs{Project: "storefront"})
				return e
			}, pulumi.WithMocks("storefront-shared", "shared", newKindeMocks()))

			errMissingOidcSrc := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := kinde.NewOidcApp(ctx, "oidc", &kinde.OidcAppArgs{
					Project:     "storefront",
					Environment: "dev",
				})
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
	})
}

// Private helpers ------------------------------------------------------------

func sampleKindePlatformArgs() *kinde.PlatformArgs {
	return &kinde.PlatformArgs{
		NonProd: kinde.TenantCredentials{
			Domain:       "nonprod-test.kinde.com",
			ClientID:     pulumi.String("nonprod-m2m-id"),
			ClientSecret: pulumi.String("nonprod-m2m-secret"),
		},
		Prod: kinde.TenantCredentials{
			Domain:       "https://prod-test.kinde.com/",
			ClientID:     pulumi.String("prod-m2m-id"),
			ClientSecret: pulumi.String("prod-m2m-secret"),
		},
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
			"nonProdIssuerUrl":            resource.NewStringProperty("https://nonprod-ref.kinde.com"),
			"nonProdClientId":             resource.NewStringProperty("ref_nonprod_cid"),
			"nonProdClientSecret":         resource.NewStringProperty("ref_nonprod_secret"),
			"prodIssuerUrl":               resource.NewStringProperty("https://prod-ref.kinde.com"),
			"prodClientId":                resource.NewStringProperty("ref_prod_cid"),
			"prodClientSecret":            resource.NewStringProperty("ref_prod_secret"),
			"kindeNonProdM2mClientId":     resource.NewStringProperty("nonprod_m2m_cid"),
			"kindeNonProdM2mClientSecret": resource.NewStringProperty("nonprod_m2m_sec"),
			"kindeProdM2mClientId":        resource.NewStringProperty("prod_m2m_cid"),
			"kindeProdM2mClientSecret":    resource.NewStringProperty("prod_m2m_sec"),
			"vercelProjectName":           resource.NewStringProperty("openbase-storefront"),
			"teamSlug":                    resource.NewStringProperty("cloudvoyant"),
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
