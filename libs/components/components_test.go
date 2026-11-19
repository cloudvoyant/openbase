package components_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

func TestUnifiedScopes(t *testing.T) {
	t.Run("given unified openbase.Platform, openbase.Project, and openbase.Environment scopes", func(t *testing.T) {
		t.Run("when registering a Vercel + Neon + Kinde Project on shared and a preview Environment, then preview inherits dev branching and Kinde NonProd tenant", func(t *testing.T) {
			runVercelProjectAndPreviewSpec(t)
		})

		t.Run("when registering a Cloud Run + GCS + Neon + Kinde Project on shared and a stage Environment, then stage branches DB from prod and binds Kinde prod tenant", func(t *testing.T) {
			runCloudRunProjectAndStageSpec(t)
		})

		t.Run("when linking a remote shared gcp.Bucket from openbaseManifest into a Vercel WebApp with WIFBroker, then it mints a federated SA and grants bucket IAM", func(t *testing.T) {
			runHybridRemoteBucketWIFSpec(t)
		})

		t.Run("when registering Project on a non-shared stack or Environment on the shared stack, then Register returns ErrInvalidEnvironment", func(t *testing.T) {
			runStackGuardrailSpec(t)
		})
	})
}

// Private helpers ------------------------------------------------------------

func runVercelProjectAndPreviewSpec(t *testing.T) {
	t.Helper()
	// Arrange.
	platMocks := newScopeMocks()
	sharedMocks := newScopeMocks()
	previewMocks := newScopeMocks()
	urlCh := make(chan string, 1)
	issuerCh := make(chan string, 1)
	var dbBranchFrom, blobFallback string

	// Act.
	errPlat := pulumi.RunErr(deployOrgPlatform, pulumi.WithMocks("platform", "platform", platMocks))
	errShared := pulumi.RunErr(deploySharedVercelProject, pulumi.WithMocks("storefront-shared", "shared", sharedMocks))
	errPreview := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return deployPreviewVercelEnv(ctx, &dbBranchFrom, &blobFallback, urlCh, issuerCh)
	}, pulumi.WithMocks("storefront", "preview-pr-42", previewMocks))

	// Assert.
	requireNoErr(t, errPlat)
	requireNoErr(t, errShared)
	requireNoErr(t, errPreview)
	platMocks.assertCount(t, "gcp:iam/workloadIdentityPool:WorkloadIdentityPool", 1)
	platMocks.assertCount(t, "gcp:iam/workloadIdentityPoolProvider:WorkloadIdentityPoolProvider", 1)
	platMocks.assertCount(t, "pulumi:providers:kinde", 2)
	sharedMocks.assertCount(t, "vercel:index/project:Project", 1)
	sharedMocks.assertCount(t, "neon:index/project:Project", 1)
	sharedMocks.assertCount(t, "kinde:index/application:Application", 2)
	sharedMocks.assertMinCount(t, "vercel:index/projectEnvironmentVariable:ProjectEnvironmentVariable", 3)
	if dbBranchFrom != "dev" || blobFallback != "dev/" {
		t.Fatalf("expected preview BranchFrom=dev and fallback=dev/, got db=%q blob=%q", dbBranchFrom, blobFallback)
	}
	if got := <-urlCh; got != "https://openbase-storefront-pr-42-cloudvoyant.vercel.app" {
		t.Fatalf("unexpected preview URL %q", got)
	}
	if got := <-issuerCh; got != "https://nonprod-ref.kinde.com" {
		t.Fatalf("unexpected preview issuer %q", got)
	}
}

func deployOrgPlatform(ctx *pulumi.Context) error {
	vercelPlat := vercel.Platform{TeamID: "team_123", TeamSlug: "cloudvoyant"}
	_, err := openbase.Platform{
		Org: "cloudvoyant",
		Resources: []core.PlatformSpecItem{
			vercelPlat,
			gcp.Platform{
				GCPProject:       pulumi.String("cloudvoyant-openbase"),
				GCPProjectNumber: "785485436615",
				Links:            []core.OIDCProviderSource{vercelPlat},
				SkipKeyRing:      true,
			},
			neon.Platform{OrgID: "org-neon", RegionID: "aws-us-east-1"},
			sampleKindePlatform(),
		},
	}.Register(ctx)
	return err
}

func deploySharedVercelProject(ctx *pulumi.Context) error {
	proj, err := openbase.NewProject(ctx, openbase.Project{
		Project:          "storefront",
		PlatformStackRef: "organization/platform/platform",
	})
	if err != nil {
		return err
	}
	authProj, err := openbase.Add(proj, kinde.Project{VercelTeamSlug: "cloudvoyant"})
	if err != nil {
		return err
	}
	if _, err := openbase.Add(proj, neon.Project{}); err != nil {
		return err
	}
	_, err = openbase.Add(proj, vercel.Project{
		EnableBlobStore: true,
		PreviewEnv: map[string]pulumi.StringInput{
			"KINDE_ISSUER_URL": authProj.NonProdIssuerURL,
			"KINDE_CLIENT_ID":  authProj.NonProdClientID,
		},
		PreviewSecrets: map[string]pulumi.StringInput{
			"KINDE_CLIENT_SECRET": authProj.NonProdClientSecret,
		},
	})
	return err
}

func deployPreviewVercelEnv(
	ctx *pulumi.Context,
	dbBranchFrom, blobFallback *string,
	urlCh, issuerCh chan string,
) error {
	env, err := openbase.NewEnvironment(ctx, openbase.Environment{Project: "storefront"})
	if err != nil {
		return err
	}
	db, err := openbase.Add(env, neon.Database{Name: "db"})
	if err != nil {
		return err
	}
	blobs, err := openbase.Add(env, vercel.BlobStore{
		Name:               "uploads",
		BlobReadWriteToken: pulumi.String("vercel_blob_rw_tok"),
	})
	if err != nil {
		return err
	}
	auth, err := openbase.Add(env, kinde.OidcApp{Name: "auth"})
	if err != nil {
		return err
	}
	web, err := openbase.Add(env, vercel.WebApp{
		Name:      "web",
		SourceDir: ".",
		Links:     []core.Resource{blobs},
		Env: map[string]pulumi.StringInput{
			"BLOB_BASE_URL":    blobs.BlobBaseURL,
			"BLOB_PREFIX":      pulumi.String(blobs.BlobPrefix),
			"KINDE_ISSUER_URL": auth.IssuerURL,
		},
		Secrets: map[string]pulumi.StringInput{
			"DATABASE_URL":        db.DatabaseURL,
			"KINDE_CLIENT_SECRET": auth.ClientSecret,
		},
	})
	if err != nil {
		return err
	}
	*dbBranchFrom = db.OpenBaseMeta().BranchFrom.String()
	*blobFallback = blobs.BlobFallbackPrefix
	web.URL.ApplyT(func(v string) string { urlCh <- v; return v })
	auth.IssuerURL.ApplyT(func(v string) string { issuerCh <- v; return v })
	return nil
}

func runCloudRunProjectAndStageSpec(t *testing.T) {
	t.Helper()
	// Arrange.
	sharedMocks := newScopeMocks()
	stageMocks := newScopeMocks()
	issuerCh := make(chan string, 1)
	var dbBranchFrom string

	// Act.
	errShared := pulumi.RunErr(deploySharedCloudRunProject, pulumi.WithMocks("orders-shared", "shared", sharedMocks))
	errStage := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return deployStageCloudRunEnv(ctx, &dbBranchFrom, issuerCh)
	}, pulumi.WithMocks("orders", "stage", stageMocks))

	// Assert.
	requireNoErr(t, errShared)
	requireNoErr(t, errStage)
	sharedMocks.assertCount(t, "gcp:artifactregistry/repository:Repository", 1)
	stageMocks.assertCount(t, "gcp:cloudrunv2/service:Service", 1)
	stageMocks.assertCount(t, "gcp:storage/bucketIAMMember:BucketIAMMember", 1)
	if dbBranchFrom != "prod" {
		t.Fatalf("expected stage neon.Database BranchFrom=prod, got %q", dbBranchFrom)
	}
	if got := <-issuerCh; got != "https://prod-ref.kinde.com" {
		t.Fatalf("expected stage prod issuer, got %q", got)
	}
}

func deploySharedCloudRunProject(ctx *pulumi.Context) error {
	cb, err := gcp.BuildCloudRunCallbackURLs("orders", "web", "785485436615", "us-central1", "", "")
	if err != nil {
		return err
	}
	_, err = openbase.Project{
		Project:          "orders",
		PlatformStackRef: "organization/platform/platform",
		Resources: []core.ScopedSpec{
			gcp.Project{},
			neon.Project{},
			kinde.Project{
				StageDomain:              cb.StageHost,
				ProdDomain:               cb.ProdHost,
				ExtraNonProdRedirectURIs: cb.ExtraNonProdRedirects,
				ExtraNonProdLogoutURIs:   cb.ExtraNonProdLogouts,
				ExtraProdRedirectURIs:    cb.ExtraProdRedirect,
				ExtraProdLogoutURIs:      cb.ExtraProdLogout,
			},
		},
	}.Register(ctx)
	return err
}

func deployStageCloudRunEnv(ctx *pulumi.Context, dbBranchFrom *string, issuerCh chan string) error {
	env, err := openbase.NewEnvironment(ctx, openbase.Environment{Project: "orders"})
	if err != nil {
		return err
	}
	db, err := openbase.Add(env, neon.Database{Name: "db"})
	if err != nil {
		return err
	}
	bkt, err := openbase.Add(env, gcp.Bucket{
		Name:            "uploads",
		GCPProject:      pulumi.String("cloudvoyant-openbase"),
		BucketNamespace: "cloudvoyant-openbase",
	})
	if err != nil {
		return err
	}
	siteURL, err := gcp.CloudRunRegionalURL("orders", core.EnvStage, "web", "785485436615", "us-central1", "")
	if err != nil {
		return err
	}
	auth, err := openbase.Add(env, kinde.OidcApp{
		Name:    "auth",
		SiteURL: pulumi.String(siteURL),
	})
	if err != nil {
		return err
	}
	_, err = openbase.Add(env, gcp.CloudRunService{
		Name:             "web",
		GCPProject:       pulumi.String("cloudvoyant-openbase"),
		GCPProjectNumber: pulumi.String("785485436615"),
		Image:            pulumi.String("us-docker.pkg.dev/cloudrun/container/hello"),
		Links:            []core.Resource{bkt},
		Env: map[string]pulumi.StringInput{
			"STORAGE_BUCKET":   bkt.BucketName,
			"KINDE_ISSUER_URL": auth.IssuerURL,
		},
		Secrets: map[string]pulumi.StringInput{
			"DATABASE_URL":        db.DatabaseURL,
			"KINDE_CLIENT_SECRET": auth.ClientSecret,
		},
	})
	if err != nil {
		return err
	}
	*dbBranchFrom = db.OpenBaseMeta().BranchFrom.String()
	auth.IssuerURL.ApplyT(func(v string) string { issuerCh <- v; return v })
	return nil
}

func runHybridRemoteBucketWIFSpec(t *testing.T) {
	t.Helper()
	// Arrange.
	mocks := newScopeMocks()
	bktNameCh := make(chan string, 1)

	// Act.
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		platRef, err := openbase.LookupPlatform(ctx)
		if err != nil {
			return err
		}
		env, err := openbase.NewEnvironment(ctx, openbase.Environment{
			Project:  "storefront",
			Platform: platRef,
			Brokers: []core.IdentityBroker{
				gcp.WIFBroker{
					GCPProject:   pulumi.String("cloudvoyant-openbase"),
					PoolName:     platRef.Output("workloadIdentityPoolName"),
					OIDCProvider: platRef.Output("vercelOidcProviderName"),
				},
			},
		})
		if err != nil {
			return err
		}
		sharedAssets := env.Project().Resource(
			"shared-assets",
			core.WithRemoteKind("gcp-bucket"),
		)
		sharedAssets.Attr("bucketName").ApplyT(func(v string) string { bktNameCh <- v; return v })
		_, err = openbase.Add(env, vercel.WebApp{
			Name:      "web",
			SourceDir: ".",
			Links:     []core.Resource{sharedAssets},
			Env: map[string]pulumi.StringInput{
				"BUCKET_NAME": sharedAssets.Attr("bucketName"),
			},
		})
		return err
	}, pulumi.WithMocks("storefront", "preview-pr-9", mocks))

	// Assert.
	requireNoErr(t, err)
	if got := <-bktNameCh; got != "cloudvoyant-openbase-storefront-shared-assets" {
		t.Fatalf("expected remote manifest BUCKET_NAME, got %q", got)
	}
	mocks.assertCount(t, "gcp:serviceaccount/account:Account", 1)
	mocks.assertCount(t, "gcp:serviceaccount/iAMMember:IAMMember", 1)
	mocks.assertCount(t, "gcp:storage/bucketIAMMember:BucketIAMMember", 1)
}

func runStackGuardrailSpec(t *testing.T) {
	t.Helper()
	// Arrange / Act.
	errProjOnDev := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := openbase.Project{Project: "storefront"}.Register(ctx)
		return err
	}, pulumi.WithMocks("storefront", "dev", newScopeMocks()))

	errEnvOnShared := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := openbase.Environment{Project: "storefront"}.Register(ctx)
		return err
	}, pulumi.WithMocks("storefront", "shared", newScopeMocks()))

	// Assert.
	if !errors.Is(errProjOnDev, core.ErrInvalidEnvironment) {
		t.Fatalf("expected ErrInvalidEnvironment for Project on dev, got %v", errProjOnDev)
	}
	if !errors.Is(errEnvOnShared, core.ErrInvalidEnvironment) {
		t.Fatalf("expected ErrInvalidEnvironment for Environment on shared, got %v", errEnvOnShared)
	}
}

func sampleKindePlatform() kinde.Platform {
	return kinde.Platform{
		NonProd: kinde.TenantCredentials{
			Domain:       "nonprod-test.kinde.com",
			ClientID:     pulumi.String("nonprod-cid"),
			ClientSecret: pulumi.String("nonprod-sec"),
		},
		Prod: kinde.TenantCredentials{
			Domain:       "prod-test.kinde.com",
			ClientID:     pulumi.String("prod-cid"),
			ClientSecret: pulumi.String("prod-sec"),
		},
	}
}

type scopeMocks struct {
	mu     sync.Mutex
	counts map[string]int
}

func newScopeMocks() *scopeMocks {
	return &scopeMocks{counts: map[string]int{}}
}

func (m *scopeMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[a.TypeToken]++
	outs := a.Inputs.Copy()
	populateScopeMockOutputs(a, outs)
	return a.Name + "_id", outs, nil
}

func (*scopeMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) {
	if a.Token == "vercel:index/getProjectDirectory:getProjectDirectory" {
		return resource.PropertyMap{
			"files": resource.NewObjectProperty(resource.PropertyMap{
				"index.html": resource.NewStringProperty("deadbeef"),
			}),
		}, nil
	}
	if a.Token == "kinde:index/getConnections:getConnections" {
		return resource.PropertyMap{
			"connections": resource.NewArrayProperty([]resource.PropertyValue{
				resource.NewObjectProperty(resource.PropertyMap{
					"id":       resource.NewStringProperty("conn_email_pw"),
					"strategy": resource.NewStringProperty("email:password"),
				}),
			}),
		}, nil
	}
	return a.Args, nil
}

func populateScopeMockOutputs(a pulumi.MockResourceArgs, outs resource.PropertyMap) {
	switch a.TypeToken {
	case "neon:index/project:Project":
		outs["branch"] = resource.NewObjectProperty(resource.PropertyMap{"id": resource.NewStringProperty("br-main")})
		outs["regionId"] = resource.NewStringProperty("aws-us-east-1")
	case "neon:index/endpoint:Endpoint":
		outs["host"] = resource.NewStringProperty("ep-test.neon.tech")
		outs["hostPooler"] = resource.NewStringProperty("ep-test-pooler.neon.tech")
	case "vercel:index/projectProtectionBypass:ProjectProtectionBypass":
		outs["secret"] = resource.NewStringProperty("bypass_secret_32_chars_minimum_ok")
	case "vercel:index/blobStore:BlobStore":
		outs["id"] = resource.NewStringProperty("store_abc123")
	case "kinde:index/application:Application":
		outs["clientId"] = resource.NewStringProperty(a.Name + "_cid")
		outs["clientSecret"] = resource.NewStringProperty(a.Name + "_csec")
	case "gcp:serviceaccount/account:Account":
		outs["email"] = resource.NewStringProperty(a.Name + "@cloudvoyant-openbase.iam.gserviceaccount.com")
		outs["name"] = resource.NewStringProperty("projects/cloudvoyant-openbase/serviceAccounts/" + a.Name)
	case "gcp:cloudrunv2/service:Service":
		outs["uri"] = resource.NewStringProperty("https://orders-stage.a.run.app")
	case "pulumi:pulumi:StackReference":
		outs["outputs"] = mockStackReferenceOutputs(a.Inputs["name"].StringValue())
	}
}

func mockStackReferenceOutputs(stackName string) resource.PropertyValue {
	if strings.HasSuffix(stackName, "/platform/platform") {
		return resource.NewObjectProperty(resource.PropertyMap{
			"gcpProject":                  resource.NewStringProperty("cloudvoyant-openbase"),
			"workloadIdentityPoolName":    resource.NewStringProperty("projects/123/locations/global/workloadIdentityPools/ob-pool"),
			"vercelOidcProviderName":      resource.NewStringProperty("projects/123/locations/global/workloadIdentityPools/ob-pool/providers/vercel-oidc"),
			"teamId":                      resource.NewStringProperty("team_123"),
			"teamSlug":                    resource.NewStringProperty("cloudvoyant"),
			"neonOrgId":                   resource.NewStringProperty("org-neon"),
			"neonRegionId":                resource.NewStringProperty("aws-us-east-1"),
			"nonProdIssuerUrl":            resource.NewStringProperty("https://nonprod-ref.kinde.com"),
			"prodIssuerUrl":               resource.NewStringProperty("https://prod-ref.kinde.com"),
			"kindeNonProdM2mClientId":     resource.NewStringProperty("nonprod-m2m-id"),
			"kindeNonProdM2mClientSecret": resource.NewStringProperty("nonprod-m2m-sec"),
			"kindeProdM2mClientId":        resource.NewStringProperty("prod-m2m-id"),
			"kindeProdM2mClientSecret":    resource.NewStringProperty("prod-m2m-sec"),
		})
	}
	return resource.NewObjectProperty(resource.PropertyMap{
		"vercelProjectId":          resource.NewStringProperty("prj_shared_123"),
		"vercelProjectName":        resource.NewStringProperty("openbase-storefront"),
		"teamSlug":                 resource.NewStringProperty("cloudvoyant"),
		"stageCustomEnvironmentId": resource.NewStringProperty("env_stage_123"),
		"blobStoreId":              resource.NewStringProperty("store_shared_blob"),
		"repositoryId":             resource.NewStringProperty("orders-shared-ar"),
		"neonProjectId":            resource.NewStringProperty("neon-proj-123"),
		"neonDefaultBranchId":      resource.NewStringProperty("br-main-123"),
		"branchId":                 resource.NewStringProperty("br-parent-123"),
		"bucketName":               resource.NewStringProperty("parent-dev-bucket"),
		"nonProdIssuerUrl":         resource.NewStringProperty("https://nonprod-ref.kinde.com"),
		"nonProdClientId":          resource.NewStringProperty("nonprod_ref_cid"),
		"nonProdClientSecret":      resource.NewStringProperty("nonprod_ref_sec"),
		"prodIssuerUrl":            resource.NewStringProperty("https://prod-ref.kinde.com"),
		"prodClientId":             resource.NewStringProperty("prod_ref_cid"),
		"prodClientSecret":         resource.NewStringProperty("prod_ref_sec"),
		core.ManifestOutputKey: resource.NewObjectProperty(resource.PropertyMap{
			"org":     resource.NewStringProperty("cloudvoyant"),
			"project": resource.NewStringProperty("storefront"),
			"resources": resource.NewObjectProperty(resource.PropertyMap{
				"shared-assets": resource.NewObjectProperty(resource.PropertyMap{
					"kind": resource.NewStringProperty("gcp-bucket"),
					"attributes": resource.NewObjectProperty(resource.PropertyMap{
						"bucketName": resource.NewStringProperty("cloudvoyant-openbase-storefront-shared-assets"),
					}),
				}),
			}),
		}),
	})
}

func (m *scopeMocks) assertCount(t *testing.T, typeToken string, want int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if got := m.counts[typeToken]; got != want {
		t.Fatalf("resource count for %s = %d, want %d", typeToken, got, want)
	}
}

func (m *scopeMocks) assertMinCount(t *testing.T, typeToken string, minWant int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if got := m.counts[typeToken]; got < minWant {
		t.Fatalf("resource count for %s = %d, want >= %d", typeToken, got, minWant)
	}
}

func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
