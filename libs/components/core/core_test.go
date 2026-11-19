package core_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

func TestEnvironmentContracts(t *testing.T) {
	t.Run("given OpenBase environment strings", func(t *testing.T) {
		t.Run("when parsing supported persistent and preview slugs, then ParseEnvironment succeeds", func(t *testing.T) {
			// Arrange.
			valid := []string{"dev", "stage", "prod", "preview-pr-42", "preview-a"}

			// Act / Assert.
			for _, raw := range valid {
				env, err := core.ParseEnvironment(raw)
				requireNoErr(t, err)
				assertEqual(t, "parsed env", env.String(), raw)
			}
		})

		t.Run("when parsing shared, malformed, or unsupported tiers, then ParseEnvironment returns ErrInvalidEnvironment", func(t *testing.T) {
			// Arrange.
			invalid := []string{"", "shared", "project", "platform", "qa", "production", "staging", "preview-", "preview--bad-", "Preview-1", "preview-UPPER"}

			// Act / Assert.
			for _, raw := range invalid {
				_, err := core.ParseEnvironment(raw)
				if !errors.Is(err, core.ErrInvalidEnvironment) {
					t.Fatalf("ParseEnvironment(%q) err = %v, want ErrInvalidEnvironment", raw, err)
				}
			}
		})

		t.Run("when inspecting lifecycle predicates", func(t *testing.T) {
			// Arrange.
			preview := core.Environment("preview-feat-auth-99")

			// Act / Assert.
			t.Run("then prod and stage are protected and persistent while dev is unprotected", func(t *testing.T) {
				assertBool(t, "prod protected", core.EnvProd.IsProtected(), true)
				assertBool(t, "stage protected", core.EnvStage.IsProtected(), true)
				assertBool(t, "dev protected", core.EnvDev.IsProtected(), false)
				assertBool(t, "dev persistent", core.EnvDev.IsPersistent(), true)
				assertBool(t, "preview persistent", preview.IsPersistent(), false)
			})

			t.Run("then preview-<slug> is ephemeral and exposes its preview-id", func(t *testing.T) {
				assertBool(t, "preview ephemeral", preview.IsEphemeral(), true)
				assertEqual(t, "preview-id", preview.PreviewID(), "feat-auth-99")
				assertEqual(t, "dev preview-id", core.EnvDev.PreviewID(), "")
			})
		})
	})
}

func TestNamingAndBranchValidation(t *testing.T) {
	t.Run("given project, environment, and resource identifiers", func(t *testing.T) {
		t.Run("when generating bounded service-account and bucket names, then lengths stay within GCP bounds deterministically", func(t *testing.T) {
			// Arrange.
			longEnv := core.Environment("preview-feature-extremely-long-branch-slug-with-issue-123456")

			// Act.
			saShort, errShort := core.ServiceAccountID("a", core.EnvDev, "b")
			saLongA, errLongA := core.ServiceAccountID("storefront", longEnv, "orders-worker-service")
			saLongB, errLongB := core.ServiceAccountID("storefront", longEnv, "orders-worker-service")
			bucket, errBucket := core.BucketName("storefront", longEnv, "uploads", "gcp-proj-123")

			// Assert.
			requireNoErr(t, errShort)
			requireNoErr(t, errLongA)
			requireNoErr(t, errLongB)
			requireNoErr(t, errBucket)
			assertBoundedID(t, "short SA", saShort, 6, 30)
			assertBoundedID(t, "long SA", saLongA, 6, 30)
			assertEqual(t, "deterministic SA", saLongA, saLongB)
			assertBoundedID(t, "bucket", bucket, 3, 63)
		})

		t.Run("when an identifier starts with a digit, then CanonicalName returns ErrInvalidIdentifier", func(t *testing.T) {
			// Arrange / Act.
			_, err := core.CanonicalName("123bad", core.EnvDev, "api")

			// Assert.
			if !errors.Is(err, core.ErrInvalidIdentifier) {
				t.Fatalf("expected ErrInvalidIdentifier, got %v", err)
			}
		})

		t.Run("when BranchFrom references an ephemeral, self, shared, or unknown tier, then Stateful.Register rejects with ErrInvalidBranchSource", func(t *testing.T) {
			// Arrange.
			cases := []struct{ target, source string }{
				{"preview-pr-1", "preview-other"},
				{"preview-pr-1", "shared"},
				{"dev", "dev"},
				{"preview-pr-1", "unknown"},
			}

			// Act / Assert.
			for _, tc := range cases {
				err := pulumi.RunErr(func(ctx *pulumi.Context) error {
					_, e := core.Stateful{
						Name:        "db",
						Project:     "orders",
						Environment: tc.target,
						BranchFrom:  tc.source,
					}.Register(ctx)
					return e
				}, pulumi.WithMocks("orders", tc.target, &noopMocks{}))
				if !errors.Is(err, core.ErrInvalidBranchSource) {
					t.Fatalf("case %+v: expected ErrInvalidBranchSource, got %v", tc, err)
				}
			}
		})
	})
}

func TestLinksAndTags(t *testing.T) {
	t.Run("given linked core.Stateful and core.Stateless specs", func(t *testing.T) {
		t.Run("when registering a preview consumer linked to a stateful resource, then tags, GCP labels, env outputs, and GrantAccess resolve", func(t *testing.T) {
			// Arrange.
			var stateMeta, appMeta core.ResourceMeta
			var gcpLabels pulumi.StringMap
			grantedCh := make(chan string, 1)
			envValCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				db, err := newLinkedTestStateful(ctx, grantedCh)
				if err != nil {
					return err
				}
				stateMeta = db.OpenBaseMeta()
				gcpLabels = core.GCPLabels(stateMeta, map[string]string{"openbase.io/custom": "v1"})
				api, err := core.Stateless{
					Name:        "api",
					Project:     "orders",
					Environment: "preview-pr-7",
					Links:       []core.Resource{db},
				}.Register(ctx)
				if err != nil {
					return err
				}
				appMeta = api.OpenBaseMeta()
				api.Link().Env["DB_HOST"].ApplyT(func(v string) string { envValCh <- v; return v })
				return nil
			}, pulumi.WithMocks("orders", "preview-pr-7", &noopMocks{}))

			// Assert.
			requireNoErr(t, err)
			assertEqual(t, "preview-id tag", stateMeta.Tags[core.TagPreviewID], "pr-7")
			assertEqual(t, "stateful tag", stateMeta.Tags[core.TagStateful], "true")
			assertEqual(t, "stateless tag", appMeta.Tags[core.TagStateful], "false")
			assertBool(t, "gcp label present", gcpLabels["openbase-preview-id"] != nil, true)
			assertEqual(t, "GrantAccess consumer", <-grantedCh, "orders-preview-pr-7-api")
			assertEqual(t, "DB_HOST link env", <-envValCh, "db.internal")
		})

		t.Run("when two linked resources export the same env key, then ResolveLinks returns ErrDuplicateLinkEnvKey", func(t *testing.T) {
			// Arrange / Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				a, err := core.Stateless{
					Name: "a", Project: "orders", Environment: "dev",
					Env: map[string]pulumi.StringInput{"SHARED_KEY": pulumi.String("1")},
				}.Register(ctx)
				if err != nil {
					return err
				}
				b, err := core.Stateless{
					Name: "b", Project: "orders", Environment: "dev",
					Env: map[string]pulumi.StringInput{"SHARED_KEY": pulumi.String("2")},
				}.Register(ctx)
				if err != nil {
					return err
				}
				_, err = core.Stateless{
					Name: "c", Project: "orders", Environment: "dev",
					Links: []core.Resource{a, b},
				}.Register(ctx)
				return err
			}, pulumi.WithMocks("orders", "dev", &noopMocks{}))

			// Assert.
			if !errors.Is(err, core.ErrDuplicateLinkEnvKey) {
				t.Fatalf("expected ErrDuplicateLinkEnvKey, got %v", err)
			}
		})
	})
}

func TestTierContracts(t *testing.T) {
	t.Run("given PlatformResource and ProjectResource registrations", func(t *testing.T) {
		t.Run("when InitPlatform and InitProject run, then each populates org or project metadata without environment coupling", func(t *testing.T) {
			// Arrange.
			var plat core.PlatformResource
			var proj core.ProjectResource

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				tp := &testPlatform{}
				if _, e := core.InitPlatform(ctx, "test:org:Platform", "org-platform", tp, &tp.PlatformMeta, core.PlatformSpec{
					Name: "gcp",
					Org:  "cloudvoyant",
				}); e != nil {
					return e
				}
				plat = tp
				tpr := &testProject{}
				if _, e := core.InitProject(ctx, "test:app:Project", "app-project", tpr, &tpr.ProjectMeta, core.ProjectSpec{
					Name:    "gcp",
					Project: "storefront",
					Repo:    "cloudvoyant/storefront",
				}); e != nil {
					return e
				}
				proj = tpr
				return nil
			}, pulumi.WithMocks("storefront-shared", "shared", &noopMocks{}))

			// Assert.
			requireNoErr(t, err)
			assertEqual(t, "platform canonical", plat.OpenBasePlatform().CanonicalName, "cloudvoyant-platform-gcp")
			assertEqual(t, "platform tier tag", plat.OpenBasePlatform().Tags[core.TagTier], core.TierPlatform)
			assertEqual(t, "project canonical", proj.OpenBaseProject().CanonicalName, "storefront-project-gcp")
			assertEqual(t, "project tier tag", proj.OpenBaseProject().Tags[core.TagTier], core.TierProject)
		})
	})
}

// Private helpers ------------------------------------------------------------

type testPlatform struct {
	pulumi.ResourceState
	core.PlatformMeta
}

type testProject struct {
	pulumi.ResourceState
	core.ProjectMeta
}

type noopMocks struct{}

func (*noopMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	return a.Name + "_id", a.Inputs, nil
}

func (*noopMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) { return a.Args, nil }

func newLinkedTestStateful(ctx *pulumi.Context, grantedCh chan<- string) (*core.CustomStatefulResource, error) {
	db, err := core.Stateful{
		Name:        "db",
		Project:     "orders",
		Repo:        "cloudvoyant/orders",
		Environment: "preview-pr-7",
		Kind:        "postgres",
		BranchFrom:  "dev",
		Env:         map[string]pulumi.StringInput{"DB_HOST": pulumi.String("db.internal")},
		Secrets:     map[string]pulumi.StringInput{"DATABASE_URL": pulumi.String("postgres://u:p@db.internal/app")},
	}.Register(ctx)
	if err != nil {
		return nil, err
	}
	desc := db.Link()
	desc.GrantAccess = func(g core.LinkGrantContext) error {
		grantedCh <- g.ConsumerName
		return nil
	}
	db.SetLink(desc)
	return db, nil
}

func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}

func assertBool(t *testing.T, label string, got, want bool) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func assertBoundedID(t *testing.T, label, id string, minLen, maxLen int) {
	t.Helper()
	if len(id) < minLen || len(id) > maxLen || strings.HasSuffix(id, "-") {
		t.Fatalf("%s %q (len=%d) outside [%d,%d] or has trailing hyphen", label, id, len(id), minLen, maxLen)
	}
}
