package components_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	gcpprov "github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/secretmanager"
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

func TestComponentsComposition(t *testing.T) {
	t.Run("given idiomatic Pulumi Go Account, Project, and Environment constructors", func(t *testing.T) {
		t.Run("when registering Vercel + Neon + Kinde on shared and a preview stack, then preview inherits dev branching and Kinde NonProd environment", func(t *testing.T) {
			runVercelProjectAndPreviewSpec(t)
		})

		t.Run("when registering Cloud Run + GCS + Neon + Kinde on shared and a stage stack, then stage branches DB from prod and binds Kinde prod environment", func(t *testing.T) {
			runCloudRunProjectAndStageSpec(t)
		})

		t.Run("when linking a remote shared gcp.Bucket from openbaseManifest into vercel.NewWebApp with WIFBroker, then it mints a federated SA and grants bucket IAM", func(t *testing.T) {
			runHybridRemoteBucketWIFSpec(t)
		})

		t.Run("when registering a shared Project resource on a non-shared stack or an unrecognized stack tier, then constructors return ErrInvalidEnvironment", func(t *testing.T) {
			runStackGuardrailSpec(t)
		})

		t.Run("when registering multiple GCP accounts and raw Pulumi resources alongside OpenBase constructors, then named LookupAccount succeeds, unnamed LookupAccount rejects ambiguity, and providers compose cleanly", func(t *testing.T) {
			runMultiGCPAndRawPulumiSpec(t)
		})
	})
}

// Private helpers ------------------------------------------------------------

func runVercelProjectAndPreviewSpec(t *testing.T) {
	t.Helper()
	// Arrange.
	platMocks := newScopeMocks(false)
	sharedMocks := newScopeMocks(false)
	previewMocks := newScopeMocks(false)
	urlCh := make(chan string, 1)
	issuerCh := make(chan string, 1)
	var dbBranchFrom, blobFallback string

	// Act.
	errPlat := pulumi.RunErr(deployOrgAccounts, pulumi.WithMocks("platform", "platform", platMocks))
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

func deployOrgAccounts(ctx *pulumi.Context) error {
	vercelAcc, err := vercel.NewAccount(ctx, "vercel", &vercel.AccountArgs{
		Org:      "cloudvoyant",
		TeamID:   "team_123",
		TeamSlug: "cloudvoyant",
	})
	if err != nil {
		return err
	}
	if _, err := gcp.NewAccount(ctx, "gcp", &gcp.AccountArgs{
		Org:              "cloudvoyant",
		GCPProject:       pulumi.String("cloudvoyant-openbase"),
		GCPProjectNumber: "785485436615",
		Links:            []core.OIDCProviderSource{vercelAcc},
		SkipKeyRing:      true,
	}); err != nil {
		return err
	}
	if _, err := neon.NewAccount(ctx, "neon", &neon.AccountArgs{
		Org:             "cloudvoyant",
		OrgID:           "org-neon",
		DefaultRegionID: "aws-us-east-1",
	}); err != nil {
		return err
	}
	_, err = kinde.NewAccount(ctx, "kinde", sampleKindeAccountArgs())
	return err
}

func deploySharedVercelProject(ctx *pulumi.Context) error {
	plat, err := openbase.LookupPlatform(ctx)
	if err != nil {
		return err
	}
	authProj, err := kinde.NewProject(ctx, "auth", &kinde.ProjectArgs{
		Account:        kinde.LookupAccount(plat),
		VercelTeamSlug: "cloudvoyant",
	})
	if err != nil {
		return err
	}
	if _, err := neon.NewProject(ctx, "db", &neon.ProjectArgs{
		Account: neon.LookupAccount(plat),
	}); err != nil {
		return err
	}
	_, err = vercel.NewProject(ctx, "web", &vercel.ProjectArgs{
		Account:         vercel.LookupAccount(plat),
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
	shared, err := openbase.LookupShared(ctx)
	if err != nil {
		return err
	}
	vProj := vercel.LookupProject(shared)
	db, err := neon.NewDatabase(ctx, "db", &neon.DatabaseArgs{
		Project: neon.LookupProject(shared),
	})
	if err != nil {
		return err
	}
	blobs, err := vercel.NewBlobStore(ctx, "uploads", &vercel.BlobStoreArgs{
		Project:            vProj,
		BlobReadWriteToken: pulumi.String("vercel_blob_rw_tok"),
	})
	if err != nil {
		return err
	}
	auth, err := kinde.NewOidcApp(ctx, "auth", &kinde.OidcAppArgs{
		Project: kinde.LookupProject(shared),
	})
	if err != nil {
		return err
	}
	web, err := vercel.NewWebApp(ctx, "web", &vercel.WebAppArgs{
		Project:   vProj,
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
	sharedMocks := newScopeMocks(false)
	stageMocks := newScopeMocks(false)
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
	plat, err := openbase.LookupPlatform(ctx)
	if err != nil {
		return err
	}
	cb, err := gcp.BuildCloudRunCallbackURLs("orders", "web", "785485436615", "us-central1", "", "")
	if err != nil {
		return err
	}
	if _, err := gcp.NewProject(ctx, "gcp", &gcp.ProjectArgs{Account: gcp.LookupAccount(plat)}); err != nil {
		return err
	}
	if _, err := neon.NewProject(ctx, "neon", &neon.ProjectArgs{Account: neon.LookupAccount(plat)}); err != nil {
		return err
	}
	_, err = kinde.NewProject(ctx, "kinde", &kinde.ProjectArgs{
		Account:                  kinde.LookupAccount(plat),
		StageDomain:              cb.StageHost,
		ProdDomain:               cb.ProdHost,
		ExtraNonProdRedirectURIs: cb.ExtraNonProdRedirects,
		ExtraNonProdLogoutURIs:   cb.ExtraNonProdLogouts,
		ExtraProdRedirectURIs:    cb.ExtraProdRedirect,
		ExtraProdLogoutURIs:      cb.ExtraProdLogout,
	})
	return err
}

func deployStageCloudRunEnv(ctx *pulumi.Context, dbBranchFrom *string, issuerCh chan string) error {
	plat, err := openbase.LookupPlatform(ctx)
	if err != nil {
		return err
	}
	shared, err := openbase.LookupShared(ctx)
	if err != nil {
		return err
	}
	gcpAcc := gcp.LookupAccount(plat)
	db, err := neon.NewDatabase(ctx, "db", &neon.DatabaseArgs{Project: neon.LookupProject(shared)})
	if err != nil {
		return err
	}
	bkt, err := gcp.NewBucket(ctx, "uploads", &gcp.BucketArgs{
		Account:         gcpAcc,
		BucketNamespace: "cloudvoyant-openbase",
	})
	if err != nil {
		return err
	}
	siteURL, err := gcp.CloudRunRegionalURL("orders", core.EnvStage, "web", "785485436615", "us-central1", "")
	if err != nil {
		return err
	}
	auth, err := kinde.NewOidcApp(ctx, "auth", &kinde.OidcAppArgs{
		Project: kinde.LookupProject(shared),
		SiteURL: pulumi.String(siteURL),
	})
	if err != nil {
		return err
	}
	_, err = gcp.NewCloudRunService(ctx, "web", &gcp.CloudRunServiceArgs{
		Account:          gcpAcc,
		Project:          gcp.LookupProject(shared),
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
	mocks := newScopeMocks(false)
	bktNameCh := make(chan string, 1)

	// Act.
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		platRef, err := openbase.LookupPlatform(ctx)
		if err != nil {
			return err
		}
		sharedRef, err := openbase.LookupShared(ctx)
		if err != nil {
			return err
		}
		gcpAcc := gcp.LookupAccount(platRef)
		sharedAssets := sharedRef.Resource(
			"shared-assets",
			core.WithRemoteKind("gcp-bucket"),
		)
		sharedAssets.Attr("bucketName").ApplyT(func(v string) string { bktNameCh <- v; return v })
		_, err = vercel.NewWebApp(ctx, "web", &vercel.WebAppArgs{
			Project:   vercel.LookupProject(sharedRef),
			SourceDir: ".",
			Links:     []core.Resource{sharedAssets},
			Brokers:   []core.IdentityBroker{gcpAcc.WIFBroker("vercel")},
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
		_, err := neon.NewProject(ctx, "neon-project", &neon.ProjectArgs{OrgID: "org-1"})
		return err
	}, pulumi.WithMocks("storefront", "dev", newScopeMocks(false)))

	errUnknownStack := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := gcp.NewBucket(ctx, "uploads", &gcp.BucketArgs{GCPProject: pulumi.String("cloudvoyant-openbase")})
		return err
	}, pulumi.WithMocks("storefront", "qa-invalid", newScopeMocks(false)))

	// Assert.
	if !errors.Is(errProjOnDev, core.ErrInvalidEnvironment) {
		t.Fatalf("expected ErrInvalidEnvironment for Project on dev, got %v", errProjOnDev)
	}
	if !errors.Is(errUnknownStack, core.ErrInvalidEnvironment) {
		t.Fatalf("expected ErrInvalidEnvironment for unrecognized stack, got %v", errUnknownStack)
	}
}

func sampleKindeAccountArgs() *kinde.AccountArgs {
	return &kinde.AccountArgs{
		Org: "cloudvoyant",
		NonProd: kinde.EnvironmentCredentials{
			Domain:       "nonprod-test.kinde.com",
			ClientID:     pulumi.String("nonprod-cid"),
			ClientSecret: pulumi.String("nonprod-sec"),
		},
		Prod: kinde.EnvironmentCredentials{
			Domain:       "prod-test.kinde.com",
			ClientID:     pulumi.String("prod-cid"),
			ClientSecret: pulumi.String("prod-sec"),
		},
	}
}

type scopeMocks struct {
	mu           sync.Mutex
	counts       map[string]int
	multiAccount bool
}

func newScopeMocks(multiAccount bool) *scopeMocks {
	return &scopeMocks{counts: map[string]int{}, multiAccount: multiAccount}
}

func (m *scopeMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[a.TypeToken]++
	outs := a.Inputs.Copy()
	populateScopeMockOutputs(a, outs, m.multiAccount)
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

func populateScopeMockOutputs(a pulumi.MockResourceArgs, outs resource.PropertyMap, multiAccount bool) {
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
		outs["outputs"] = mockStackReferenceOutputs(a.Inputs["name"].StringValue(), multiAccount)
	}
}

func mockStackReferenceOutputs(stackName string, multiAccount bool) resource.PropertyValue {
	if strings.HasSuffix(stackName, "/platform/platform") {
		return mockPlatformStackOutputs(multiAccount)
	}
	return mockSharedStackOutputs()
}

func mockPlatformStackOutputs(multiAccount bool) resource.PropertyValue {
	gcpResources := resource.PropertyMap{
		"gcp": resource.NewObjectProperty(resource.PropertyMap{
			"kind": resource.NewStringProperty("gcp-account"),
			"attributes": resource.NewObjectProperty(resource.PropertyMap{
				"gcpProject":               resource.NewStringProperty("cloudvoyant-openbase"),
				"workloadIdentityPoolName": resource.NewStringProperty("projects/123/locations/global/workloadIdentityPools/ob-pool"),
				"vercelOidcProviderName":   resource.NewStringProperty("projects/123/locations/global/workloadIdentityPools/ob-pool/providers/vercel-oidc"),
			}),
		}),
	}
	if multiAccount {
		gcpResources["gcp-analytics"] = resource.NewObjectProperty(resource.PropertyMap{
			"kind": resource.NewStringProperty("gcp-account"),
			"attributes": resource.NewObjectProperty(resource.PropertyMap{
				"gcpProject": resource.NewStringProperty("cloudvoyant-analytics"),
				"baseDomain": resource.NewStringProperty("analytics.acme.io"),
			}),
		})
	}
	return resource.NewObjectProperty(resource.PropertyMap{
		"gcpProject":                  resource.NewStringProperty("cloudvoyant-openbase"),
		"workloadIdentityPoolName":    resource.NewStringProperty("projects/123/locations/global/workloadIdentityPools/ob-pool"),
		"vercelOidcProviderName":      resource.NewStringProperty("projects/123/locations/global/workloadIdentityPools/ob-pool/providers/vercel-oidc"),
		"teamId":                      resource.NewStringProperty("team_123"),
		"teamSlug":                    resource.NewStringProperty("cloudvoyant"),
		"neonOrgId":                   resource.NewStringProperty("org-neon"),
		"neonDefaultRegionId":         resource.NewStringProperty("aws-us-east-1"),
		"nonProdIssuerUrl":            resource.NewStringProperty("https://nonprod-ref.kinde.com"),
		"prodIssuerUrl":               resource.NewStringProperty("https://prod-ref.kinde.com"),
		"kindeNonProdM2mClientId":     resource.NewStringProperty("nonprod-m2m-id"),
		"kindeNonProdM2mClientSecret": resource.NewStringProperty("nonprod-m2m-sec"),
		"kindeProdM2mClientId":        resource.NewStringProperty("prod-m2m-id"),
		"kindeProdM2mClientSecret":    resource.NewStringProperty("prod-m2m-sec"),
		core.ManifestOutputKey: resource.NewObjectProperty(resource.PropertyMap{
			"org":       resource.NewStringProperty("cloudvoyant"),
			"resources": resource.NewObjectProperty(gcpResources),
		}),
	})
}

func mockSharedStackOutputs() resource.PropertyValue {
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

func runMultiGCPAndRawPulumiSpec(t *testing.T) {
	t.Helper()
	// Arrange.
	platMocks := newScopeMocks(true)
	sharedMocks := newScopeMocks(true)
	envMocks := newScopeMocks(true)
	analyticsRepoURL := make(chan string, 1)

	// Act.
	errPlat := pulumi.RunErr(deployMultiAccountGCPPlatform, pulumi.WithMocks("platform", "platform", platMocks))
	errAmbig := pulumi.RunErr(func(ctx *pulumi.Context) error {
		plat, err := openbase.LookupPlatform(ctx)
		if err != nil {
			return err
		}
		ctx.Export("ambigProj", gcp.LookupAccount(plat).GCPProject)
		return nil
	}, pulumi.WithMocks("orders-shared", "shared", newScopeMocks(true)))
	errShared := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return deployMultiProjectGCPShared(ctx, analyticsRepoURL)
	}, pulumi.WithMocks("orders-shared", "shared", sharedMocks))
	errEnv := pulumi.RunErr(deployEnvWithRawPulumi, pulumi.WithMocks("orders", "dev", envMocks))

	// Assert.
	requireNoErr(t, errPlat)
	if !errors.Is(errAmbig, core.ErrAmbiguousResource) {
		t.Fatalf("expected ErrAmbiguousResource on unnamed LookupAccount with 2 accounts, got %v", errAmbig)
	}
	requireNoErr(t, errShared)
	requireNoErr(t, errEnv)
	platMocks.assertCount(t, "pulumi:providers:gcp", 1)
	platMocks.assertCount(t, "gcp:iam/workloadIdentityPool:WorkloadIdentityPool", 2)
	sharedMocks.assertCount(t, "gcp:artifactregistry/repository:Repository", 2)
	envMocks.assertCount(t, "gcp:storage/bucket:Bucket", 2)
	envMocks.assertCount(t, "gcp:secretmanager/secret:Secret", 1)
	envMocks.assertCount(t, "gcp:secretmanager/secretIamMember:SecretIamMember", 1)
	if got := <-analyticsRepoURL; !strings.Contains(got, "/cloudvoyant-analytics/") {
		t.Fatalf("expected analytics AR repo in cloudvoyant-analytics, got %q", got)
	}
}

func deployMultiAccountGCPPlatform(ctx *pulumi.Context) error {
	analyticsProv, err := gcpprov.NewProvider(ctx, "gcp-analytics-account", &gcpprov.ProviderArgs{
		Project: pulumi.String("cloudvoyant-analytics"),
	})
	if err != nil {
		return err
	}
	if _, err := gcp.NewAccount(ctx, "gcp", &gcp.AccountArgs{
		Org:         "cloudvoyant",
		GCPProject:  pulumi.String("cloudvoyant-openbase"),
		SkipKeyRing: true,
	}); err != nil {
		return err
	}
	_, err = gcp.NewAccount(ctx, "gcp-analytics", &gcp.AccountArgs{
		Org:         "cloudvoyant",
		GCPProject:  pulumi.String("cloudvoyant-analytics"),
		BaseDomain:  "analytics.acme.io",
		SkipKeyRing: true,
	}, pulumi.Provider(analyticsProv))
	return err
}

func deployMultiProjectGCPShared(ctx *pulumi.Context, analyticsRepoURL chan<- string) error {
	plat, err := openbase.LookupPlatform(ctx)
	if err != nil {
		return err
	}
	if _, err := gcp.NewProject(ctx, "gcp-core", &gcp.ProjectArgs{
		Account: gcp.LookupAccount(plat, "gcp"),
	}); err != nil {
		return err
	}
	analyticsAR, err := gcp.NewProject(ctx, "gcp-analytics", &gcp.ProjectArgs{
		Account: gcp.LookupAccount(plat, "gcp-analytics"),
	})
	if err != nil {
		return err
	}
	analyticsAR.RepositoryURL.ApplyT(func(v string) string { analyticsRepoURL <- v; return v })
	return nil
}

func deployEnvWithRawPulumi(ctx *pulumi.Context) error {
	plat, err := openbase.LookupPlatform(ctx)
	if err != nil {
		return err
	}
	coreAcc := gcp.LookupAccount(plat, "gcp")
	analyticsAcc := gcp.LookupAccount(plat, "gcp-analytics")
	uploads, err := gcp.NewBucket(ctx, "uploads", &gcp.BucketArgs{
		Account: coreAcc,
	})
	if err != nil {
		return err
	}
	if _, err := gcp.NewBucket(ctx, "analytics-events", &gcp.BucketArgs{
		Account: analyticsAcc,
	}); err != nil {
		return err
	}
	rawSecret, err := secretmanager.NewSecret(ctx, "raw-partner-key", &secretmanager.SecretArgs{
		Project:     coreAcc.GCPProject,
		SecretId:    pulumi.String("orders-dev-raw-partner-key"),
		Replication: &secretmanager.SecretReplicationArgs{Auto: &secretmanager.SecretReplicationAutoArgs{}},
	})
	if err != nil {
		return err
	}
	svc, err := gcp.NewCloudRunService(ctx, "api", &gcp.CloudRunServiceArgs{
		Account: coreAcc,
		Image:   pulumi.String("us-central1-docker.pkg.dev/cloudvoyant-openbase/orders/api:v1"),
		Links:   []core.Resource{uploads},
		Env:     map[string]pulumi.StringInput{"RAW_SECRET_ID": rawSecret.SecretId},
	})
	if err != nil {
		return err
	}
	_, err = secretmanager.NewSecretIamMember(ctx, "raw-partner-key-accessor", &secretmanager.SecretIamMemberArgs{
		Project:  coreAcc.GCPProject,
		SecretId: rawSecret.SecretId,
		Role:     pulumi.String("roles/secretmanager.secretAccessor"),
		Member:   pulumi.Sprintf("serviceAccount:%s", svc.ServiceAccountEmail),
	})
	return err
}
