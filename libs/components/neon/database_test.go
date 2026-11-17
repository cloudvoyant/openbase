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
	t.Run("given neon.SharedProject and neon.Database specs", func(t *testing.T) {
		t.Run("when registering SharedProject and a root prod Database, then it creates Role/Database and exports pooled and direct URLs", func(t *testing.T) {
			// Arrange.
			mocks := newNeonMocks()
			pooledCh := make(chan string, 1)
			directCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				shared, err := neon.SharedProject{
					Project:  "storefront",
					OrgID:    "org-test",
					RegionID: "aws-us-east-1",
				}.Register(ctx)
				if err != nil {
					return err
				}
				db, err := neon.Database{
					Name:            "db",
					Project:         "storefront",
					Environment:     "prod",
					ProjectID:       shared.ProjectID,
					DefaultBranchID: shared.DefaultBranchID,
				}.Register(ctx)
				if err != nil {
					return err
				}
				db.Link().Secrets["DATABASE_URL"].ApplyT(func(v string) string { pooledCh <- v; return v })
				db.Link().Secrets["DIRECT_URL"].ApplyT(func(v string) string { directCh <- v; return v })
				return nil
			}, pulumi.WithMocks("storefront", "prod", mocks))

			// Assert.
			requireNeonNoErr(t, err)
			mocks.assertCount(t, "neon:index/project:Project", 1)
			mocks.assertCount(t, "neon:index/branch:Branch", 1)
			mocks.assertCount(t, "neon:index/endpoint:Endpoint", 1)
			mocks.assertCount(t, "neon:index/role:Role", 1)
			mocks.assertCount(t, "neon:index/database:Database", 1)
			if pooled := <-pooledCh; !strings.Contains(pooled, "-pooler.neon.tech") {
				t.Fatalf("expected pooled host in DATABASE_URL, got %q", pooled)
			}
			if direct := <-directCh; strings.Contains(direct, "-pooler") {
				t.Fatalf("expected unpooled host in DIRECT_URL, got %q", direct)
			}
		})

		t.Run("when branching a preview Database from dev, then it inherits parent Role/Database via GetBranchRolePassword", func(t *testing.T) {
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
			if got := <-urlCh; !strings.Contains(got, "inherited-secret") {
				t.Fatalf("expected inherited role password in preview URL, got %q", got)
			}
		})

		t.Run("when ProjectID and SharedStackRef are omitted, then Register returns ErrProjectIDRequired", func(t *testing.T) {
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
		outs["defaultBranchId"] = resource.NewStringProperty("br-main")
		outs["regionId"] = resource.NewStringProperty("aws-us-east-1")
	case "neon:index/endpoint:Endpoint":
		outs["host"] = resource.NewStringProperty("ep-test.neon.tech")
		outs["hostPooling"] = resource.NewStringProperty("ep-test-pooler.neon.tech")
	case "neon:index/role:Role":
		outs["password"] = resource.NewStringProperty("root-secret")
	}
	return a.Name + "_id", outs, nil
}

func (*neonMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) {
	if a.Token == "neon:index/getBranchRolePassword:getBranchRolePassword" {
		return resource.PropertyMap{
			"branchId":  a.Args["branchId"],
			"projectId": a.Args["projectId"],
			"roleName":  a.Args["roleName"],
			"password":  resource.NewStringProperty("inherited-secret"),
			"id":        resource.NewStringProperty("pw-id"),
		}, nil
	}
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
