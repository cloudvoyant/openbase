package gcp_test

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/gcp"
)

// Public API -----------------------------------------------------------------

func TestPlatformAndProject(t *testing.T) {
	t.Run("given gcp.Platform and gcp.Project specs", func(t *testing.T) {
		t.Run("when two gcp.Project apps share one gcp.Platform, then APIs, WIF pool, and KeyRing are created once while each app gets its own Artifact Registry", func(t *testing.T) {
			// Arrange.
			mocks := newGCPMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				plat, err := gcp.Platform{
					Project:        "cloudvoyant",
					GCPProject:     pulumi.String("cloudvoyant-openbase"),
					GitHubOrg:      "cloudvoyant",
					VercelTeamSlug: "cloudvoyant",
				}.Register(ctx)
				if err != nil {
					return err
				}
				appA, err := gcp.Project{
					Name:       "gcp-a",
					Project:    "storefront",
					GCPProject: pulumi.String("cloudvoyant-openbase"),
					Platform:   plat,
				}.Register(ctx)
				if err != nil {
					return err
				}
				appB, err := gcp.Project{
					Name:       "gcp-b",
					Project:    "billing",
					GCPProject: pulumi.String("cloudvoyant-openbase"),
					Platform:   plat,
				}.Register(ctx)
				if err != nil {
					return err
				}
				assertProjectNotNil(t, appA)
				assertProjectNotNil(t, appB)
				return nil
			}, pulumi.WithMocks("cloudvoyant", "shared", mocks))

			// Assert.
			requireGCPNoErr(t, err)
			mocks.assertCount(t, "gcp:projects/service:Service", 8)
			mocks.assertCount(t, "gcp:artifactregistry/repository:Repository", 2)
			mocks.assertCount(t, "gcp:iam/workloadIdentityPool:WorkloadIdentityPool", 1)
			mocks.assertCount(t, "gcp:iam/workloadIdentityPoolProvider:WorkloadIdentityPoolProvider", 2)
			mocks.assertCount(t, "gcp:kms/keyRing:KeyRing", 1)
		})

		t.Run("when standalone gcp.Project sets SkipKeyRing and VercelIssuerMode global, then it provisions inline Platform without KeyRing", func(t *testing.T) {
			// Arrange.
			mocks := newGCPMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := gcp.Project{
					Project:          "storefront",
					GCPProject:       pulumi.String("cloudvoyant-openbase"),
					VercelTeamSlug:   "cloudvoyant",
					VercelIssuerMode: "global",
					SkipKeyRing:      true,
					Services:         []string{"run.googleapis.com", "storage.googleapis.com"},
				}.Register(ctx)
				return err
			}, pulumi.WithMocks("storefront", "shared", mocks))

			// Assert.
			requireGCPNoErr(t, err)
			mocks.assertCount(t, "gcp:projects/service:Service", 2)
			mocks.assertCount(t, "gcp:kms/keyRing:KeyRing", 0)
			mocks.assertCount(t, "gcp:iam/workloadIdentityPoolProvider:WorkloadIdentityPoolProvider", 1)
		})
	})
}

// Private helpers ------------------------------------------------------------

type gcpMockRecord struct {
	typeToken string
	name      string
	inputs    resource.PropertyMap
}

type gcpMocks struct {
	mu      sync.Mutex
	records []gcpMockRecord
}

func newGCPMocks() *gcpMocks { return &gcpMocks{} }

func (m *gcpMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	outs := a.Inputs.Copy()
	populateGCPMockOutputs(a, outs)
	m.records = append(m.records, gcpMockRecord{typeToken: a.TypeToken, name: a.Name, inputs: outs})
	return a.Name + "_id", outs, nil
}

func (*gcpMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return a.Args, nil
}

func populateGCPMockOutputs(a pulumi.MockResourceArgs, outs resource.PropertyMap) {
	if _, ok := outs["project"]; !ok {
		outs["project"] = resource.NewStringProperty("cloudvoyant-openbase")
	}
	switch a.TypeToken {
	case "gcp:serviceaccount/account:Account":
		accountID := outs["accountId"].StringValue()
		outs["email"] = resource.NewStringProperty(accountID + "@cloudvoyant-openbase.iam.gserviceaccount.com")
		outs["name"] = resource.NewStringProperty("projects/cloudvoyant-openbase/serviceAccounts/" + accountID)
	case "gcp:cloudrunv2/service:Service":
		outs["uri"] = resource.NewStringProperty("https://" + a.Name + ".a.run.app")
	case "gcp:storage/bucket:Bucket":
		outs["url"] = resource.NewStringProperty("gs://" + outs["name"].StringValue())
	case "gcp:iam/workloadIdentityPool:WorkloadIdentityPool":
		poolID := outs["workloadIdentityPoolId"].StringValue()
		outs["name"] = resource.NewStringProperty("projects/123456/locations/global/workloadIdentityPools/" + poolID)
	}
}

func (m *gcpMocks) assertCount(t *testing.T, typeToken string, want int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	got := 0
	for _, r := range m.records {
		if r.typeToken == typeToken {
			got++
		}
	}
	if got != want {
		t.Fatalf("resource count for %s = %d, want %d", typeToken, got, want)
	}
}

func (m *gcpMocks) findFirst(typeToken string) (gcpMockRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.records {
		if r.typeToken == typeToken {
			return r, true
		}
	}
	return gcpMockRecord{}, false
}

func assertProjectNotNil(t *testing.T, s *gcp.ProjectResource) {
	t.Helper()
	if s.Repository == nil || s.WorkloadIdentityPool == nil || s.GitHubOIDCProvider == nil || s.VercelOIDCProvider == nil || s.KeyRing == nil {
		t.Fatal("expected Repository, WorkloadIdentityPool, GitHubOIDCProvider, VercelOIDCProvider, and KeyRing to be non-nil")
	}
}

func requireGCPNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
