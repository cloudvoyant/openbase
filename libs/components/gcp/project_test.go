package gcp_test

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
)

// Public API -----------------------------------------------------------------

func TestAccountAndProject(t *testing.T) {
	t.Run("given separate gcp.Account (platform stack) and gcp.Project (shared stack) specs", func(t *testing.T) {
		t.Run("when gcp.Account runs on the platform stack and two gcp.Project apps run on shared stacks, then APIs, WIF pool, and KeyRing live on platform while each app gets its own Artifact Registry", func(t *testing.T) {
			// Arrange.
			platMocks := newGCPMocks()
			sharedMocks := newGCPMocks()

			// Act.
			errPlat := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := gcp.NewAccount(ctx, "gcp", &gcp.AccountArgs{
					Org:        "cloudvoyant",
					GCPProject: pulumi.String("cloudvoyant-openbase"),
					Links: []core.OIDCProviderSource{
						core.GitHubActionsOIDC("cloudvoyant"),
						core.OIDCIssuer{
							ID:                 "vercel",
							DisplayName:        "Vercel OIDC",
							IssuerURI:          "https://oidc.vercel.com/cloudvoyant",
							AllowedAudiences:   []string{"https://vercel.com/cloudvoyant"},
							AttributeCondition: "assertion.owner == 'cloudvoyant'",
							Claims:             map[string]string{"project_id": "assertion.project_id"},
						},
					},
				})
				return err
			}, pulumi.WithMocks("platform", "platform", platMocks))

			errShared := pulumi.RunErr(func(ctx *pulumi.Context) error {
				plat, err := openbase.LookupPlatform(ctx)
				if err != nil {
					return err
				}
				gcpAcc := gcp.LookupAccount(plat)
				appA, err := gcp.NewProject(ctx, "gcp-a", &gcp.ProjectArgs{
					ProjectName: "storefront",
					Account:     gcpAcc,
				})
				if err != nil {
					return err
				}
				appB, err := gcp.NewProject(ctx, "gcp-b", &gcp.ProjectArgs{
					ProjectName: "billing",
					Account:     gcpAcc,
				})
				if err != nil {
					return err
				}
				assertProjectNotNil(t, appA)
				assertProjectNotNil(t, appB)
				return nil
			}, pulumi.WithMocks("storefront-shared", "shared", sharedMocks))

			// Assert.
			requireGCPNoErr(t, errPlat)
			requireGCPNoErr(t, errShared)
			platMocks.assertCount(t, "gcp:projects/service:Service", 8)
			platMocks.assertCount(t, "gcp:iam/workloadIdentityPool:WorkloadIdentityPool", 1)
			platMocks.assertCount(t, "gcp:iam/workloadIdentityPoolProvider:WorkloadIdentityPoolProvider", 2)
			platMocks.assertCount(t, "gcp:kms/keyRing:KeyRing", 1)
			sharedMocks.assertCount(t, "gcp:artifactregistry/repository:Repository", 2)
			sharedMocks.assertCount(t, "gcp:iam/workloadIdentityPool:WorkloadIdentityPool", 0)
		})

		t.Run("when gcp.Account sets SkipKeyRing and custom Services, then it omits KeyRing and enables only requested APIs", func(t *testing.T) {
			// Arrange.
			mocks := newGCPMocks()

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, err := gcp.NewAccount(ctx, "gcp", &gcp.AccountArgs{
					Org:        "cloudvoyant",
					GCPProject: pulumi.String("cloudvoyant-openbase"),
					Links: []core.OIDCProviderSource{
						core.OIDCIssuer{
							ID:               "custom",
							IssuerURI:        "https://oidc.example.com",
							AllowedAudiences: []string{"https://example.com"},
						},
					},
					SkipKeyRing: true,
					Services:    []string{"run.googleapis.com", "storage.googleapis.com"},
				})
				return err
			}, pulumi.WithMocks("platform", "platform", mocks))

			// Assert.
			requireGCPNoErr(t, err)
			mocks.assertCount(t, "gcp:projects/service:Service", 2)
			mocks.assertCount(t, "gcp:kms/keyRing:KeyRing", 0)
			mocks.assertCount(t, "gcp:iam/workloadIdentityPool:WorkloadIdentityPool", 1)
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
	case "pulumi:pulumi:StackReference":
		outs["outputs"] = resource.NewObjectProperty(resource.PropertyMap{
			"gcpProject":    resource.NewStringProperty("cloudvoyant-openbase"),
			"gcpBaseDomain": resource.NewStringProperty("gcp.cloudvoyant.io"),
		})
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

func assertProjectNotNil(t *testing.T, s *gcp.Project) {
	t.Helper()
	if s.Repository == nil {
		t.Fatal("expected Artifact Registry Repository to be non-nil on gcp.Project")
	}
}

func requireGCPNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
