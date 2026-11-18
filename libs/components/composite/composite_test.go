package composite_test

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/composite"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

// Public API -----------------------------------------------------------------

func TestCompositeApps(t *testing.T) {
	t.Run("given composite.VercelNeonApp specs", func(t *testing.T) {
		t.Run("when registered on shared, then it provisions Vercel Shared, Neon SharedProject, and Kinde SharedOidcApp", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := sampleVercelComposite("shared").Register(ctx)
				return e
			}, pulumi.WithMocks("storefront", "shared", mocks))

			// Assert.
			requireCompNoErr(t, err)
			mocks.assertCount(t, "vercel:index/project:Project", 1)
			mocks.assertCount(t, "vercel:index/blobStore:BlobStore", 1)
			mocks.assertCount(t, "neon:index/project:Project", 1)
			mocks.assertCount(t, "kinde:index/application:Application", 2)
		})

		t.Run("when registered on preview-pr-20, then it wires Neon CoW, Blob overlay, and Kinde OidcApp into WebApp via StackReferences", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()
			urlCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				app, e := sampleVercelComposite("preview-pr-20").Register(ctx)
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
			mocks.assertCount(t, "neon:index/branch:Branch", 1)
			mocks.assertCount(t, "vercel:index/deployment:Deployment", 1)
			mocks.assertCount(t, "vercel:index/alias:Alias", 1)
		})
	})

	t.Run("given composite.GcpCloudRunNeonApp specs", func(t *testing.T) {
		t.Run("when registered on shared, then it provisions GCP Shared, Neon SharedProject, and Kinde SharedOidcApp", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := sampleGCPComposite("shared").Register(ctx)
				return e
			}, pulumi.WithMocks("storefront", "shared", mocks))

			// Assert.
			requireCompNoErr(t, err)
			mocks.assertCount(t, "gcp:artifactregistry/repository:Repository", 1)
			mocks.assertCount(t, "neon:index/project:Project", 1)
			mocks.assertCount(t, "kinde:index/application:Application", 2)
		})

		t.Run("when registered on preview-pr-21, then it links GCS overlay, Neon CoW, and Secret Manager Kinde auth into CloudRunService", func(t *testing.T) {
			// Arrange.
			mocks := newCompositeMocks()
			regCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				app, e := sampleGCPComposite("preview-pr-21").Register(ctx)
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
			mocks.assertCount(t, "gcp:storage/bucket:Bucket", 1)
			mocks.assertCount(t, "neon:index/branch:Branch", 1)
			mocks.assertCount(t, "gcp:cloudrunv2/service:Service", 1)
		})
	})
}

// Private helpers ------------------------------------------------------------

func sampleKindeConfig() *composite.KindeConfig {
	return &composite.KindeConfig{
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

func sampleVercelComposite(env string) composite.VercelNeonApp {
	return composite.VercelNeonApp{
		Name:               "web",
		Project:            "storefront",
		Environment:        env,
		VercelProjectName:  "openbase-storefront",
		TeamID:             "team_123",
		TeamSlug:           "cloudvoyant",
		SourceDir:          ".",
		BlobReadWriteToken: pulumi.String("vercel_blob_rw_tok"),
		Auth:               sampleKindeConfig(),
	}
}

func sampleGCPComposite(env string) composite.GcpCloudRunNeonApp {
	return composite.GcpCloudRunNeonApp{
		Name:             "web",
		Project:          "storefront",
		Environment:      env,
		GCPProject:       "cloudvoyant-openbase",
		GCPProjectNumber: "785485436615",
		SkipKeyRing:      true,
		Image:            pulumi.String("mirror.gcr.io/library/python:3.12-slim"),
		Auth:             sampleKindeConfig(),
	}
}

type compositeMocks struct {
	mu     sync.Mutex
	counts map[string]int
}

func newCompositeMocks() *compositeMocks {
	return &compositeMocks{counts: map[string]int{}}
}

func (m *compositeMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[a.TypeToken]++
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

func requireCompNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
