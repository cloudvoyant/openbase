package vercel_test

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

func TestVercelProjectAndPlatform(t *testing.T) {
	t.Run("given separate vercel.Platform (platform stack) and vercel.Project (shared stack) specs", func(t *testing.T) {
		t.Run("when Platform runs on platform and Project runs on shared with EnableBlobStore, then Project inherits team/baseDomain via PlatformStackRef and creates Project, CustomEnvironment, ProjectDomains, Bypass, and BlobStore", func(t *testing.T) {
			// Arrange.
			platMocks := newVercelMocks()
			sharedMocks := newVercelMocks()
			baseURLCh := make(chan string, 1)

			// Act.
			errPlat := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := vercel.Platform{
					TeamID:     "team_123",
					TeamSlug:   "cloudvoyant",
					BaseDomain: "vercel.cloudvoyant.io",
				}.Register(ctx)
				return err
			}, pulumi.WithMocks("platform", "platform", platMocks))

			errShared := pulumi.RunErr(func(ctx *pulumi.Context) error {
				proj, err := vercel.Project{
					Project:          "storefront",
					PlatformStackRef: "organization/platform/platform",
					EnableBlobStore:  true,
				}.Register(ctx)
				if err != nil {
					return err
				}
				proj.BlobBaseURL.ApplyT(func(v string) string { baseURLCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront-shared", "shared", sharedMocks))

			// Assert.
			requireVercelNoErr(t, errPlat)
			requireVercelNoErr(t, errShared)
			sharedMocks.assertCount(t, "vercel:index/project:Project", 1)
			sharedMocks.assertCount(t, "vercel:index/customEnvironment:CustomEnvironment", 1)
			sharedMocks.assertCount(t, "vercel:index/projectDomain:ProjectDomain", 2)
			sharedMocks.assertCount(t, "vercel:index/projectProtectionBypass:ProjectProtectionBypass", 1)
			sharedMocks.assertCount(t, "vercel:index/blobStore:BlobStore", 1)
			sharedMocks.assertCount(t, "vercel:index/blobProjectConnection:BlobProjectConnection", 1)
			assertVercelEq(t, "derived BLOB_BASE_URL", <-baseURLCh, "https://abcxyz.public.blob.vercel-storage.com")
		})

		t.Run("when BaseDomain, EnableDevEnvironment, and ExtraProdDomains are set, then it creates dev and staging CustomEnvironments and attaches all tier domains", func(t *testing.T) {
			// Arrange.
			mocks := newVercelMocks()
			prodDomCh := make(chan string, 1)
			devDomCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				proj, err := vercel.Project{
					Project:              "storefront",
					TeamID:               "team_123",
					TeamSlug:             "cloudvoyant",
					BaseDomain:           "vercel.cloudvoyant.io",
					EnableDevEnvironment: true,
					ExtraProdDomains:     []string{"storefront.io"},
				}.Register(ctx)
				if err != nil {
					return err
				}
				proj.ProdDomainName.ApplyT(func(v string) string { prodDomCh <- v; return v })
				proj.DevDomainName.ApplyT(func(v string) string { devDomCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "shared", mocks))

			// Assert.
			requireVercelNoErr(t, err)
			assertVercelEq(t, "prod domain", <-prodDomCh, "storefront-prod.vercel.cloudvoyant.io")
			assertVercelEq(t, "dev domain", <-devDomCh, "storefront-dev.vercel.cloudvoyant.io")
			mocks.assertCount(t, "vercel:index/customEnvironment:CustomEnvironment", 2)
			mocks.assertCount(t, "vercel:index/projectDomain:ProjectDomain", 4)
		})

		t.Run("when SkipStageEnvironment is set for zero-custom-env plans, then it creates 0 CustomEnvironments and attaches only prod domain", func(t *testing.T) {
			// Arrange.
			mocks := newVercelMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := vercel.Project{
					Project:              "storefront",
					TeamID:               "team_123",
					TeamSlug:             "cloudvoyant",
					SkipStageEnvironment: true,
				}.Register(ctx)
				return err
			}, pulumi.WithMocks("storefront", "shared", mocks))

			// Assert.
			requireVercelNoErr(t, err)
			mocks.assertCount(t, "vercel:index/customEnvironment:CustomEnvironment", 0)
			mocks.assertCount(t, "vercel:index/projectDomain:ProjectDomain", 1)
		})
	})
}

// Private helpers ------------------------------------------------------------

type vercelMocks struct {
	mu     sync.Mutex
	counts map[string]int
}

func newVercelMocks() *vercelMocks {
	return &vercelMocks{counts: map[string]int{}}
}

func (m *vercelMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[a.TypeToken]++
	outs := a.Inputs.Copy()
	id := a.Name + "_id"
	switch a.TypeToken {
	case "vercel:index/blobStore:BlobStore":
		id = "store_AbcXyz"
	case "vercel:index/projectProtectionBypass:ProjectProtectionBypass":
		outs["secret"] = resource.NewStringProperty("bypass_secret_32_chars_000000000")
	case "vercel:index/project:Project":
		outs["teamId"] = resource.NewStringProperty("team_123")
	case "gcp:serviceaccount/account:Account":
		acc := outs["accountId"].StringValue()
		outs["email"] = resource.NewStringProperty(acc + "@proj.iam.gserviceaccount.com")
		outs["name"] = resource.NewStringProperty("projects/proj/serviceAccounts/" + acc)
	case "pulumi:pulumi:StackReference":
		outs["outputs"] = resource.NewObjectProperty(resource.PropertyMap{
			"teamId":                   resource.NewStringProperty("team_123"),
			"vercelBaseDomain":         resource.NewStringProperty("vercel.cloudvoyant.io"),
			"projectId":                resource.NewStringProperty("prj_from_ref"),
			"vercelProjectName":        resource.NewStringProperty("openbase-storefront"),
			"teamSlug":                 resource.NewStringProperty("cloudvoyant"),
			"stageCustomEnvironmentId": resource.NewStringProperty("env_stage_ref"),
			"blobStoreId":              resource.NewStringProperty("store_AbcXyz"),
		})
	}
	return id, outs, nil
}

func (*vercelMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) {
	if a.Token == "vercel:index/getProjectDirectory:getProjectDirectory" {
		return resource.PropertyMap{
			"path": a.Args["path"],
			"id":   resource.NewStringProperty("dir_1"),
			"files": resource.NewObjectProperty(resource.PropertyMap{
				"api/index.js": resource.NewStringProperty("12:deadbeef"),
			}),
		}, nil
	}
	return a.Args, nil
}

func (m *vercelMocks) assertCount(t *testing.T, typeToken string, want int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if got := m.counts[typeToken]; got != want {
		t.Fatalf("resource count for %s = %d, want %d", typeToken, got, want)
	}
}

func requireVercelNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertVercelEq(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}
