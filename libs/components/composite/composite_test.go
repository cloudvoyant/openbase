package composite_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/composite"
	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

func TestPlatformComposite(t *testing.T) {
	t.Run("given composite.Platform and downstream Project specs", func(t *testing.T) {
		t.Run("when composite.Platform is registered on shared, then it provisions org-wide GCP, Vercel, Neon, and Kinde foundations once", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := sampleOrgPlatform().Register(ctx)
				return e
			}, pulumi.WithMocks("platform", "shared", mocks))

			// Assert.
			requireCompNoErr(t, err)
			mocks.assertCount(t, "gcp:iam/workloadIdentityPool:WorkloadIdentityPool", 1)
			mocks.assertCount(t, "gcp:compute/globalForwardingRule:GlobalForwardingRule", 1)
			mocks.assertCount(t, "vercel:index/sharedEnvironmentVariable:SharedEnvironmentVariable", 1)
			mocks.assertCount(t, "neon:index/orgApiKey:OrgApiKey", 1)
			mocks.assertCount(t, "pulumi:providers:kinde", 2)
		})

		t.Run("when GcpCloudRunNeonProject and VercelNeonProject inherit composite.Platform, then they reuse org WIF/tenants without duplicating pools", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				plat, e := sampleOrgPlatform().Register(ctx)
				if e != nil {
					return e
				}
				vSpec := sampleVercelProject("")
				vSpec.Platform = plat
				if _, e := vSpec.Register(ctx); e != nil {
					return e
				}
				gSpec := sampleGCPProject("")
				gSpec.Platform = plat
				_, e = gSpec.Register(ctx)
				return e
			}, pulumi.WithMocks("storefront-shared", "shared", mocks))

			// Assert.
			requireCompNoErr(t, err)
			mocks.assertCount(t, "gcp:iam/workloadIdentityPool:WorkloadIdentityPool", 1)
			mocks.assertCount(t, "gcp:artifactregistry/repository:Repository", 1)
			mocks.assertCount(t, "vercel:index/project:Project", 1)
			mocks.assertCount(t, "neon:index/project:Project", 2)
			mocks.assertCount(t, "kinde:index/application:Application", 4)
		})
	})
}

func TestVercelComposite(t *testing.T) {
	t.Run("given composite.VercelNeonProject and composite.VercelNeonApp specs", func(t *testing.T) {
		t.Run("when VercelNeonProject is registered on shared, then it provisions vercel.Project, neon.Project, kinde.Project, and preview env vars", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := sampleVercelProject("").Register(ctx)
				return e
			}, pulumi.WithMocks("storefront-shared", "shared", mocks))

			// Assert.
			requireCompNoErr(t, err)
			mocks.assertCount(t, "vercel:index/project:Project", 1)
			mocks.assertCount(t, "vercel:index/blobStore:BlobStore", 1)
			mocks.assertCount(t, "vercel:index/projectEnvironmentVariable:ProjectEnvironmentVariable", 5)
			mocks.assertCount(t, "neon:index/project:Project", 1)
			mocks.assertCount(t, "kinde:index/application:Application", 2)
		})

		t.Run("when VercelNeonProject is registered on dev or VercelNeonApp on shared, then both reject mismatched stacks with ErrInvalidEnvironment", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()

			// Act.
			projectOnDevErr := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := sampleVercelProject("dev").Register(ctx)
				return e
			}, pulumi.WithMocks("storefront-shared", "dev", mocks))
			appOnSharedErr := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := sampleVercelApp("shared").Register(ctx)
				return e
			}, pulumi.WithMocks("storefront", "shared", mocks))

			// Assert.
			if !errors.Is(projectOnDevErr, core.ErrInvalidEnvironment) {
				t.Fatalf("expected ErrInvalidEnvironment for VercelNeonProject on dev, got %v", projectOnDevErr)
			}
			if !errors.Is(appOnSharedErr, core.ErrInvalidEnvironment) {
				t.Fatalf("expected ErrInvalidEnvironment for VercelNeonApp on shared, got %v", appOnSharedErr)
			}
		})

		t.Run("when VercelNeonApp is registered on preview-pr-20, then it wires Neon CoW, Blob overlay, and Kinde OidcApp into WebApp via <project>-shared/shared StackReference", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()
			urlCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				app, e := sampleVercelApp("preview-pr-20").Register(ctx)
				if e != nil {
					return e
				}
				app.URL.ApplyT(func(v string) string { urlCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "preview-pr-20", mocks))

			// Assert.
			requireCompNoErr(t, err)
			if got := <-urlCh; got != "https://openbase-storefront-pr-20-cloudvoyant.vercel.app" {
				t.Fatalf("preview URL = %q", got)
			}
			mocks.assertStackRef(t, "organization/storefront-shared/shared")
			mocks.assertCount(t, "neon:index/branch:Branch", 1)
			mocks.assertCount(t, "vercel:index/deployment:Deployment", 1)
			mocks.assertCount(t, "vercel:index/alias:Alias", 1)
		})

		t.Run("when VercelNeonApp uses BaseDomain and ExtraDomains, then it binds {project}-{previewId}.{baseDomain} and extra vanity aliases", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()
			urlCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				spec := sampleVercelApp("preview-pr-20")
				spec.BaseDomain = "vercel.cloudvoyant.io"
				spec.ExtraDomains = []string{"pr20.storefront.io"}
				app, e := spec.Register(ctx)
				if e != nil {
					return e
				}
				app.URL.ApplyT(func(v string) string { urlCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "preview-pr-20", mocks))

			// Assert.
			requireCompNoErr(t, err)
			if got := <-urlCh; got != "https://storefront-pr-20.vercel.cloudvoyant.io" {
				t.Fatalf("BaseDomain preview URL = %q", got)
			}
			mocks.assertCount(t, "vercel:index/alias:Alias", 2)
			mocks.assertCount(t, "vercel:index/projectDomain:ProjectDomain", 0)
		})
	})
}

func TestGCPComposite(t *testing.T) {
	t.Run("given composite.GcpCloudRunNeonProject and composite.GcpCloudRunNeonApp specs", func(t *testing.T) {
		t.Run("when GcpCloudRunNeonProject is registered on shared, then it provisions gcp.Project, neon.Project, and kinde.Project", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := sampleGCPProject("").Register(ctx)
				return e
			}, pulumi.WithMocks("storefront-shared", "shared", mocks))

			// Assert.
			requireCompNoErr(t, err)
			mocks.assertCount(t, "gcp:artifactregistry/repository:Repository", 1)
			mocks.assertCount(t, "neon:index/project:Project", 1)
			mocks.assertCount(t, "kinde:index/application:Application", 2)
		})

		t.Run("when GcpCloudRunNeonApp is registered on preview-pr-21, then it links GCS overlay, Neon CoW, and Secret Manager Kinde auth into CloudRunService via <project>-shared/shared StackReference", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()
			regCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				app, e := sampleGCPApp("preview-pr-21").Register(ctx)
				if e != nil {
					return e
				}
				app.RegionalURL.ApplyT(func(v string) string { regCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "preview-pr-21", mocks))

			// Assert.
			requireCompNoErr(t, err)
			if got := <-regCh; got != "https://storefront-preview-pr-21-web-785485436615.us-central1.run.app" {
				t.Fatalf("regionalUrl = %q", got)
			}
			mocks.assertStackRef(t, "organization/storefront-shared/shared")
			mocks.assertCount(t, "gcp:storage/bucket:Bucket", 1)
			mocks.assertCount(t, "neon:index/branch:Branch", 1)
			mocks.assertCount(t, "gcp:cloudrunv2/service:Service", 1)
		})

		t.Run("when GcpCloudRunNeonProject enables BaseDomain + LoadBalancer and GcpCloudRunNeonApp sets BaseDomain + Domains, then both wire gcp.cloudvoyant.io and extra DomainMappings", func(t *testing.T) {
			// Arrange.
			projectMocks := newCompositeMocks()
			appMocks := newCompositeMocks()
			urlCh := make(chan string, 1)

			// Act.
			projErr := pulumi.RunErr(func(ctx *pulumi.Context) error {
				spec := sampleGCPProject("")
				spec.BaseDomain = "gcp.cloudvoyant.io"
				spec.EnableLoadBalancer = true
				_, e := spec.Register(ctx)
				return e
			}, pulumi.WithMocks("storefront-shared", "shared", projectMocks))
			appErr := pulumi.RunErr(func(ctx *pulumi.Context) error {
				spec := sampleGCPApp("stage")
				spec.BaseDomain = "gcp.cloudvoyant.io"
				spec.Domains = []string{"stage.storefront.io"}
				app, e := spec.Register(ctx)
				if e != nil {
					return e
				}
				app.URL.ApplyT(func(v string) string { urlCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "stage", appMocks))

			// Assert.
			requireCompNoErr(t, projErr)
			requireCompNoErr(t, appErr)
			if got := <-urlCh; got != "https://storefront-stage.gcp.cloudvoyant.io" {
				t.Fatalf("BaseDomain stage URL = %q", got)
			}
			projectMocks.assertCount(t, "gcp:compute/globalForwardingRule:GlobalForwardingRule", 1)
			appMocks.assertCount(t, "gcp:cloudrun/domainMapping:DomainMapping", 1)
		})
	})
}

// Private helpers ------------------------------------------------------------

func sampleOrgPlatform() composite.Platform {
	return composite.Platform{
		Project: "platform",
		GCP: &gcp.Platform{
			GCPProject:         pulumi.String("cloudvoyant-openbase"),
			GCPProjectNumber:   "785485436615",
			GitHubOrg:          "cloudvoyant",
			VercelTeamSlug:     "cloudvoyant",
			BaseDomain:         "gcp.cloudvoyant.io",
			EnableLoadBalancer: true,
			SkipKeyRing:        true,
		},
		Vercel: &vercel.Platform{
			TeamID:     "team_123",
			TeamSlug:   "cloudvoyant",
			BaseDomain: "vercel.cloudvoyant.io",
			SharedEnv:  map[string]pulumi.StringInput{"ORG_ENV": pulumi.String("cloudvoyant")},
		},
		Neon: &neon.Platform{
			OrgID:           "org_123",
			RegionID:        "aws-us-east-1",
			CreateOrgAPIKey: true,
		},
		Kinde: &kinde.Platform{
			Dev: kinde.TenantCredentials{
				Domain:       "dev-test.kinde.com",
				ClientID:     pulumi.String("dev-cid"),
				ClientSecret: pulumi.String("dev-csec"),
			},
			Prod: kinde.TenantCredentials{
				Domain:       "prod-test.kinde.com",
				ClientID:     pulumi.String("prod-cid"),
				ClientSecret: pulumi.String("prod-csec"),
			},
		},
	}
}

func sampleKindeProjectConfig() *composite.KindeProjectConfig {
	return &composite.KindeProjectConfig{
		Dev: kinde.TenantCredentials{
			Domain:       "dev-test.kinde.com",
			ClientID:     pulumi.String("dev-cid"),
			ClientSecret: pulumi.String("dev-csec"),
		},
		Prod: kinde.TenantCredentials{
			Domain:       "prod-test.kinde.com",
			ClientID:     pulumi.String("prod-cid"),
			ClientSecret: pulumi.String("prod-csec"),
		},
	}
}

func sampleVercelProject(env string) composite.VercelNeonProject {
	return composite.VercelNeonProject{
		Project:           "storefront",
		Environment:       env,
		VercelProjectName: "openbase-storefront",
		TeamID:            "team_123",
		TeamSlug:          "cloudvoyant",
		Auth:              sampleKindeProjectConfig(),
	}
}

func sampleVercelApp(env string) composite.VercelNeonApp {
	return composite.VercelNeonApp{
		Name:               "web",
		Project:            "storefront",
		Environment:        env,
		VercelProjectName:  "openbase-storefront",
		TeamID:             "team_123",
		TeamSlug:           "cloudvoyant",
		SourceDir:          "../apps/web",
		BlobReadWriteToken: pulumi.String("vercel_blob_rw_tok"),
		Auth:               &composite.KindeAppConfig{},
	}
}

func sampleGCPProject(env string) composite.GcpCloudRunNeonProject {
	return composite.GcpCloudRunNeonProject{
		Project:          "storefront",
		Environment:      env,
		GCPProject:       "cloudvoyant-openbase",
		GCPProjectNumber: "785485436615",
		SkipKeyRing:      true,
		Auth:             sampleKindeProjectConfig(),
	}
}

func sampleGCPApp(env string) composite.GcpCloudRunNeonApp {
	return composite.GcpCloudRunNeonApp{
		Name:             "web",
		Project:          "storefront",
		Environment:      env,
		GCPProject:       "cloudvoyant-openbase",
		GCPProjectNumber: "785485436615",
		Image:            pulumi.String("mirror.gcr.io/library/python:3.12-slim"),
		Auth:             &composite.KindeAppConfig{},
	}
}

type compositeMocks struct {
	mu        sync.Mutex
	counts    map[string]int
	stackRefs map[string]bool
}

func newCompositeMocks() *compositeMocks {
	return &compositeMocks{
		counts:    map[string]int{},
		stackRefs: map[string]bool{},
	}
}

func (m *compositeMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[a.TypeToken]++
	if a.TypeToken == "pulumi:pulumi:StackReference" && a.Inputs["name"].IsString() {
		m.stackRefs[a.Inputs["name"].StringValue()] = true
	}
	outs := populateMockOutputs(a)
	id := a.Name + "_id"
	if a.TypeToken == "vercel:index/blobStore:BlobStore" {
		id = "store_AbcXyz"
	}
	return id, outs, nil
}

func populateMockOutputs(a pulumi.MockResourceArgs) resource.PropertyMap {
	outs := a.Inputs.Copy()
	switch a.TypeToken {
	case "vercel:index/projectProtectionBypass:ProjectProtectionBypass":
		outs["secret"] = resource.NewStringProperty("bypass_secret_32_chars_000000000")
	case "neon:index/project:Project":
		outs["defaultBranchId"] = resource.NewStringProperty("br_main_123")
	case "neon:index/orgApiKey:OrgApiKey":
		outs["key"] = resource.NewStringProperty("napi_org_mock_123")
	case "neon:index/endpoint:Endpoint":
		outs["host"] = resource.NewStringProperty("ep-test.us-east-1.aws.neon.tech")
		outs["hostPooling"] = resource.NewStringProperty("ep-test-pooler.us-east-1.aws.neon.tech")
	case "kinde:index/application:Application":
		outs["clientId"] = resource.NewStringProperty(a.Name + "_cid")
		outs["clientSecret"] = resource.NewStringProperty(a.Name + "_csec")
	case "gcp:serviceaccount/account:Account":
		acc := outs["accountId"].StringValue()
		outs["email"] = resource.NewStringProperty(acc + "@proj.iam.gserviceaccount.com")
		outs["name"] = resource.NewStringProperty("projects/proj/serviceAccounts/" + acc)
	case "gcp:storage/bucket:Bucket":
		outs["url"] = resource.NewStringProperty("gs://" + outs["name"].StringValue())
	case "gcp:cloudrunv2/service:Service":
		outs["uri"] = resource.NewStringProperty("https://storefront-web-abc-uc.a.run.app")
	case "pulumi:pulumi:StackReference":
		outs["outputs"] = mockStackRefOutputs()
	}
	return outs
}

func mockStackRefOutputs() resource.PropertyValue {
	return resource.NewObjectProperty(resource.PropertyMap{
		"vercelProjectId":          resource.NewStringProperty("prj_123"),
		"vercelProjectName":        resource.NewStringProperty("openbase-storefront"),
		"teamSlug":                 resource.NewStringProperty("cloudvoyant"),
		"stageCustomEnvironmentId": resource.NewStringProperty("env_stage_123"),
		"blobStoreId":              resource.NewStringProperty("store_AbcXyz"),
		"neonProjectId":            resource.NewStringProperty("neon_proj_123"),
		"neonDefaultBranchId":      resource.NewStringProperty("br_main_123"),
		"branchId":                 resource.NewStringProperty("br_dev_123"),
		"bucketName":               resource.NewStringProperty("cloudvoyant-openbase-storefront-dev-uploads"),
		"repositoryId":             resource.NewStringProperty("storefront-shared-gcp"),
		"workloadIdentityPoolName": resource.NewStringProperty("projects/785/locations/global/workloadIdentityPools/ob-pool"),
		"devIssuerUrl":             resource.NewStringProperty("https://dev-test.kinde.com"),
		"devClientId":              resource.NewStringProperty("dev_cid"),
		"devClientSecret":          resource.NewStringProperty("dev_csec"),
		"prodIssuerUrl":            resource.NewStringProperty("https://prod-test.kinde.com"),
		"prodClientId":             resource.NewStringProperty("prod_cid"),
		"prodClientSecret":         resource.NewStringProperty("prod_csec"),
	})
}

func (*compositeMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) {
	switch a.Token {
	case "vercel:index/getProjectDirectory:getProjectDirectory":
		return resource.PropertyMap{
			"path":  a.Args["path"],
			"id":    resource.NewStringProperty("dir_1"),
			"files": resource.NewObjectProperty(resource.PropertyMap{"api/app.js": resource.NewStringProperty("12:deadbeef")}),
		}, nil
	case "neon:index/getBranchRolePassword:getBranchRolePassword":
		return resource.PropertyMap{"password": resource.NewStringProperty("pw_mock_secret")}, nil
	case "kinde:index/getConnections:getConnections":
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

func (m *compositeMocks) assertCount(t *testing.T, typeToken string, want int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if got := m.counts[typeToken]; got != want {
		t.Fatalf("resource count for %s = %d, want %d", typeToken, got, want)
	}
}

func (m *compositeMocks) assertStackRef(t *testing.T, want string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.stackRefs[want] {
		t.Fatalf("expected StackReference %q, saw %v", want, m.stackRefs)
	}
}

func requireCompNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
