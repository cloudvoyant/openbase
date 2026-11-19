package neon_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/neon"
)

// Public API -----------------------------------------------------------------

func TestNeonDatabase(t *testing.T) {
	t.Run("given separate neon.Platform (platform), neon.Project (shared), and neon.Database (env) stacks", func(t *testing.T) {
		t.Run("when registering Platform on platform, Project on shared, and root Database on prod, then each stack creates its scoped resources and exposes pooled and direct URLs", func(t *testing.T) {
			// Arrange.
			platMocks := newNeonMocks()
			sharedMocks := newNeonMocks()
			prodMocks := newNeonMocks()
			pooledCh := make(chan string, 1)
			directCh := make(chan string, 1)

			// Act.
			errPlat := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := neon.Platform{
					OrgID:    "org-test",
					RegionID: "aws-us-east-1",
				}.Register(ctx)
				return err
			}, pulumi.WithMocks("platform", "platform", platMocks))

			errShared := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := neon.Project{
					Project:          "storefront",
					PlatformStackRef: "organization/platform/platform",
				}.Register(ctx)
				return err
			}, pulumi.WithMocks("storefront-shared", "shared", sharedMocks))

			errProd := pulumi.RunErr(func(ctx *pulumi.Context) error {
				db, err := neon.Database{
					Name:            "db",
					Project:         "storefront",
					Environment:     "prod",
					ProjectStackRef: "organization/storefront-shared/shared",
				}.Register(ctx)
				if err != nil {
					return err
				}
				db.DatabaseURL.ApplyT(func(v string) string { pooledCh <- v; return v })
				db.DirectURL.ApplyT(func(v string) string { directCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "prod", prodMocks))

			// Assert.
			requireNeonNoErr(t, errPlat)
			requireNeonNoErr(t, errShared)
			requireNeonNoErr(t, errProd)
			sharedMocks.assertCount(t, "neon:index/project:Project", 1)
			prodMocks.assertCount(t, "neon:index/branch:Branch", 1)
			prodMocks.assertCount(t, "neon:index/endpoint:Endpoint", 1)
			prodMocks.assertCount(t, "neon:index/role:Role", 1)
			prodMocks.assertCount(t, "neon:index/database:Database", 1)
			if pooled := <-pooledCh; !strings.Contains(pooled, "-pooler.neon.tech") {
				t.Fatalf("expected pooled host in DATABASE_URL, got %q", pooled)
			}
			if direct := <-directCh; strings.Contains(direct, "-pooler") {
				t.Fatalf("expected unpooled host in DIRECT_URL, got %q", direct)
			}
		})

		t.Run("when branching a preview Database from dev, then it inherits parent Role/Database without recreating them", func(t *testing.T) {
			// Arrange.
			mocks := newNeonMocks()
			urlCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				db, err := neon.Database{
					Name:           "db",
					Project:        "storefront",
					Environment:    "preview-pr-12",
					BranchFrom:     "dev",
					ProjectID:      pulumi.String("proj-123"),
					ParentBranchID: pulumi.String("br-dev-123"),
				}.Register(ctx)
				if err != nil {
					return err
				}
				db.DatabaseURL.ApplyT(func(v string) string { urlCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "preview-pr-12", mocks))

			// Assert.
			requireNeonNoErr(t, err)
			mocks.assertCount(t, "neon:index/branch:Branch", 1)
			mocks.assertCount(t, "neon:index/endpoint:Endpoint", 1)
			mocks.assertCount(t, "neon:index/role:Role", 0)
			mocks.assertCount(t, "neon:index/database:Database", 0)
			if got := <-urlCh; !strings.Contains(got, "mock-neon-role-password") {
				t.Fatalf("expected role password in preview URL, got %q", got)
			}
		})

		t.Run("when NeonProject, ProjectID, and ProjectStackRef are omitted, then Register returns ErrProjectIDRequired", func(t *testing.T) {
			// Arrange / Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := neon.Database{
					Name:        "db",
					Project:     "storefront",
					Environment: "dev",
				}.Register(ctx)
				return e
			}, pulumi.WithMocks("storefront", "dev", newNeonMocks()))

			// Assert.
			if !errors.Is(err, neon.ErrProjectIDRequired) {
				t.Fatalf("expected ErrProjectIDRequired, got %v", err)
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

type neonMocks struct {
	mu     sync.Mutex
	counts map[string]int
}

func newNeonMocks() *neonMocks {
	return &neonMocks{counts: map[string]int{}}
}

func (m *neonMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[a.TypeToken]++
	outs := a.Inputs.Copy()
	switch a.TypeToken {
	case "neon:index/project:Project":
		outs["branch"] = resource.NewObjectProperty(resource.PropertyMap{
			"id": resource.NewStringProperty("br-main"),
		})
		outs["regionId"] = resource.NewStringProperty("aws-us-east-1")
	case "neon:index/endpoint:Endpoint":
		outs["host"] = resource.NewStringProperty("ep-test.neon.tech")
		outs["hostPooler"] = resource.NewStringProperty("ep-test-pooler.neon.tech")
	case "pulumi:pulumi:StackReference":
		outs["outputs"] = resource.NewObjectProperty(resource.PropertyMap{
			"neonOrgId":           resource.NewStringProperty("org-test"),
			"neonRegionId":        resource.NewStringProperty("aws-us-east-1"),
			"neonProjectId":       resource.NewStringProperty("proj-from-ref"),
			"neonDefaultBranchId": resource.NewStringProperty("br-main-ref"),
		})
	}
	return a.Name + "_id", outs, nil
}

func (*neonMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return a.Args, nil
}

func (m *neonMocks) assertCount(t *testing.T, typeToken string, want int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if got := m.counts[typeToken]; got != want {
		t.Fatalf("resource count for %s = %d, want %d", typeToken, got, want)
	}
}

func requireNeonNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
