package vercel_test

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

func TestVercelShared(t *testing.T) {
	t.Run("given a vercel.Shared spec with EnableBlobStore", func(t *testing.T) {
		t.Run("when registered in the shared stack, then it creates Project, CustomEnvironment, ProjectDomains, Bypass, and BlobStore", func(t *testing.T) {
			// Arrange.
			mocks := newVercelMocks()
			baseURLCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				shared, err := vercel.Shared{
					Project:         "storefront",
					TeamID:          "team_123",
					TeamSlug:        "cloudvoyant",
					EnableBlobStore: true,
				}.Register(ctx)
				if err != nil {
					return err
				}
				shared.BlobBaseURL.ApplyT(func(v string) string { baseURLCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "shared", mocks))

			// Assert.
			requireVercelNoErr(t, err)
			mocks.assertCount(t, "vercel:index/project:Project", 1)
			mocks.assertCount(t, "vercel:index/customEnvironment:CustomEnvironment", 1)
			mocks.assertCount(t, "vercel:index/projectDomain:ProjectDomain", 2)
			mocks.assertCount(t, "vercel:index/projectProtectionBypass:ProjectProtectionBypass", 1)
			mocks.assertCount(t, "vercel:index/blobStore:BlobStore", 1)
			mocks.assertCount(t, "vercel:index/blobProjectConnection:BlobProjectConnection", 1)
			assertVercelEq(t, "derived BLOB_BASE_URL", <-baseURLCh, "https://abcxyz.public.blob.vercel-storage.com")
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
			"projectId":                resource.NewStringProperty("prj_from_ref"),
			"vercelProjectName":        resource.NewStringProperty("openbase-storefront"),
			"teamSlug":                 resource.NewStringProperty("cloudvoyant"),
			"stageCustomEnvironmentId": resource.NewStringProperty("env_stage_ref"),
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
