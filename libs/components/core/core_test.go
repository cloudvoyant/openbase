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
		t.Run("when parsing supported persistent, shared, and preview slugs, then ParseEnvironment succeeds", func(t *testing.T) {
			// Arrange.
			valid := []string{"shared", "dev", "stage", "prod", "preview-pr-42", "preview-a"}

			// Act / Assert.
			for _, raw := range valid {
				env, err := core.ParseEnvironment(raw)
				requireNoErr(t, err)
				assertEqual(t, "parsed env", env.String(), raw)
			}
		})

		t.Run("when parsing malformed or unsupported tiers, then ParseEnvironment returns ErrInvalidEnvironment", func(t *testing.T) {
			// Arrange.
			invalid := []string{"", "project", "platform", "qa", "production", "staging", "preview-", "preview--bad-", "Preview-1", "preview-UPPER"}

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

		t.Run("when CanonicalName is called with EnvShared vs an environment, then shared omits env from naming", func(t *testing.T) {
			// Arrange / Act.
			sharedName, errShared := core.CanonicalName("storefront", core.EnvShared, "gcp")
			devName, errDev := core.CanonicalName("storefront", core.EnvDev, "gcp")
			sharedHost, errHost := core.HostLabel("storefront", core.EnvShared, "relay")

			// Assert.
			requireNoErr(t, errShared)
			requireNoErr(t, errDev)
			requireNoErr(t, errHost)
			assertEqual(t, "shared canonical", sharedName, "storefront-gcp")
			assertEqual(t, "dev canonical", devName, "storefront-dev-gcp")
			assertEqual(t, "shared host label", sharedHost, "storefront-relay")
		})

		t.Run("when an identifier starts with a digit, then CanonicalName returns ErrInvalidIdentifier", func(t *testing.T) {
			// Arrange / Act.
			_, err := core.CanonicalName("123bad", core.EnvDev, "api")

			// Assert.
			if !errors.Is(err, core.ErrInvalidIdentifier) {
				t.Fatalf("expected ErrInvalidIdentifier, got %v", err)
			}
		})

		t.Run("when BranchFrom references an ephemeral, self, shared, or unknown tier, then NewStateful rejects with ErrInvalidBranchSource", func(t *testing.T) {
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
					_, e := core.NewStateful(ctx, "db", &core.StatefulArgs{
						Project:     "orders",
						Environment: tc.target,
						BranchFrom:  tc.source,
					})
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
		t.Run("when registering a preview consumer linked to multiple stateful resources, then tags, GCP labels, attributes, and GrantAccess resolve without collisions", func(t *testing.T) {
			// Arrange.
			var stateMeta, appMeta core.ResourceMeta
			var gcpLabels pulumi.StringMap
			grantedCh := make(chan string, 2)
			attrCh := make(chan string, 1)

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				dbA, err := newLinkedTestStateful(ctx, "db-primary", grantedCh)
				if err != nil {
					return err
				}
				dbB, err := newLinkedTestStateful(ctx, "db-analytics", grantedCh)
				if err != nil {
					return err
				}
				stateMeta = dbA.OpenBaseMeta()
				gcpLabels = core.GCPLabels(stateMeta, map[string]string{"openbase.io/custom": "v1"})
				api, err := core.NewStateless(ctx, "api", &core.StatelessArgs{
					Project:     "orders",
					Environment: "preview-pr-7",
					Links:       []core.Resource{dbA, dbB},
				})
				if err != nil {
					return err
				}
				appMeta = api.OpenBaseMeta()
				dbA.Link().Attributes["host"].ApplyT(func(v string) string { attrCh <- v; return v })
				return nil
			}, pulumi.WithMocks("orders", "preview-pr-7", &noopMocks{}))

			// Assert.
			requireNoErr(t, err)
			assertEqual(t, "preview-id tag", stateMeta.Tags[core.TagPreviewID], "pr-7")
			assertEqual(t, "stateful tag", stateMeta.Tags[core.TagStateful], "true")
			assertEqual(t, "stateless tag", appMeta.Tags[core.TagStateful], "false")
			assertBool(t, "gcp label present", gcpLabels["openbase-preview-id"] != nil, true)
			assertEqual(t, "first GrantAccess consumer", <-grantedCh, "orders-preview-pr-7-api")
			assertEqual(t, "second GrantAccess consumer", <-grantedCh, "orders-preview-pr-7-api")
			assertEqual(t, "attribute host", <-attrCh, "db.internal")
		})
	})
}

func TestPlatformContract(t *testing.T) {
	t.Run("given a PlatformResource registration", func(t *testing.T) {
		t.Run("when InitPlatform runs, then it populates org metadata distinct from ResourceMeta", func(t *testing.T) {
			// Arrange.
			var plat core.PlatformResource

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
				return nil
			}, pulumi.WithMocks("platform", "shared", &noopMocks{}))

			// Assert.
			requireNoErr(t, err)
			assertEqual(t, "platform canonical", plat.OpenBasePlatform().CanonicalName, "cloudvoyant-gcp")
			assertEqual(t, "platform tier tag", plat.OpenBasePlatform().Tags[core.TagTier], core.TierPlatform)
		})
	})
}

// Private helpers ------------------------------------------------------------

type testPlatform struct {
	pulumi.ResourceState
	core.PlatformMeta
}

type noopMocks struct{}

func (*noopMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	return a.Name + "_id", a.Inputs, nil
}

func (*noopMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) { return a.Args, nil }

func newLinkedTestStateful(ctx *pulumi.Context, name string, grantedCh chan<- string) (*core.Stateful, error) {
	return core.NewStateful(ctx, name, &core.StatefulArgs{
		Project:     "orders",
		Repo:        "cloudvoyant/orders",
		Environment: "preview-pr-7",
		Kind:        "postgres",
		BranchFrom:  "dev",
		Attributes:  map[string]pulumi.StringInput{"host": pulumi.String("db.internal")},
		GrantAccess: func(g core.LinkGrantContext) error {
			grantedCh <- g.ConsumerName
			return nil
		},
	})
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
