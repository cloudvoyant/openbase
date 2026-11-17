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

func TestParseValidEnvironments(t *testing.T) {
	t.Run("when parsing valid environments", func(t *testing.T) {
		for _, raw := range []string{"shared", "dev", "stage", "prod", "preview-pr-42", "preview-a"} {
			t.Run("for "+raw, func(t *testing.T) {
				// Arrange / Act.
				env, err := core.ParseEnvironment(raw)

				// Assert.
				requireNoErr(t, err)
				if env.String() != raw {
					t.Fatalf("env.String() = %q, want %q", env.String(), raw)
				}
			})
		}
	})
}

func TestParseMalformedEnvironments(t *testing.T) {
	t.Run("when parsing malformed environments", func(t *testing.T) {
		for _, invalid := range []string{"", "qa", "production", "staging", "preview-", "preview--bad-", "Preview-1", "preview-UPPER"} {
			t.Run("for "+invalid, func(t *testing.T) {
				// Arrange / Act.
				_, err := core.ParseEnvironment(invalid)

				// Assert.
				if !errors.Is(err, core.ErrInvalidEnvironment) {
					t.Fatalf("ParseEnvironment(%q) err = %v, want ErrInvalidEnvironment", invalid, err)
				}
			})
		}
	})
}

func TestEnvironmentPredicates(t *testing.T) {
	t.Run("when inspecting environment lifecycle predicates", func(t *testing.T) {
		// Arrange.
		preview := core.Environment("preview-feat-auth-99")

		// Act / Assert.
		t.Run("it marks prod and stage protected and persistent", func(t *testing.T) {
			assertBool(t, "prod protected", core.EnvProd.IsProtected(), true)
			assertBool(t, "stage protected", core.EnvStage.IsProtected(), true)
			assertBool(t, "dev protected", core.EnvDev.IsProtected(), false)
			assertBool(t, "dev persistent", core.EnvDev.IsPersistent(), true)
			assertBool(t, "preview persistent", preview.IsPersistent(), false)
		})
		t.Run("it marks preview ephemeral and extracts preview-id", func(t *testing.T) {
			assertBool(t, "preview ephemeral", preview.IsEphemeral(), true)
			assertEqual(t, "preview-id", preview.PreviewID(), "feat-auth-99")
			assertEqual(t, "dev preview-id", core.EnvDev.PreviewID(), "")
		})
	})
}

func TestServiceAccountAndBucketNaming(t *testing.T) {
	t.Run("when generating bounded service-account and bucket identifiers", func(t *testing.T) {
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

	t.Run("when rejecting invalid identifiers", func(t *testing.T) {
		// Arrange / Act.
		_, err := core.CanonicalName("123bad", core.EnvDev, "api")

		// Assert.
		if !errors.Is(err, core.ErrInvalidIdentifier) {
			t.Fatalf("expected ErrInvalidIdentifier, got %v", err)
		}
	})
}

func TestInvalidBranchFrom(t *testing.T) {
	t.Run("when BranchFrom is invalid", func(t *testing.T) {
		cases := []struct{ target, source string }{
			{"preview-pr-1", "preview-other"},
			{"preview-pr-1", "shared"},
			{"dev", "dev"},
			{"preview-pr-1", "unknown"},
		}
		for _, tc := range cases {
			t.Run(tc.source+"_into_"+tc.target, func(t *testing.T) {
				// Arrange / Act.
				err := pulumi.RunErr(func(ctx *pulumi.Context) error {
					_, e := core.NewStatefulResource(ctx, "db", core.StatefulResourceArgs{
						Project:     "orders",
						Environment: tc.target,
						BranchFrom:  tc.source,
					})
					return e
				}, pulumi.WithMocks("orders", tc.target, &noopMocks{}))

				// Assert.
				if !errors.Is(err, core.ErrInvalidBranchSource) {
					t.Fatalf("expected ErrInvalidBranchSource, got %v", err)
				}
			})
		}
	})
}

func TestLinksAndTags(t *testing.T) {
	t.Run("when wiring Stateful and Stateless resources through Links", func(t *testing.T) {
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
			api, err := core.NewStatelessResource(ctx, "api", core.StatelessResourceArgs{
				Project:     "orders",
				Environment: "preview-pr-7",
				Links:       []core.Resource{db},
			})
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
}

func TestDuplicateLinkKey(t *testing.T) {
	t.Run("when two linked resources export the same env key", func(t *testing.T) {
		// Arrange / Act.
		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			a, err := core.NewStatelessResource(ctx, "a", core.StatelessResourceArgs{
				Project: "orders", Environment: "dev",
				Env: map[string]pulumi.StringInput{"SHARED_KEY": pulumi.String("1")},
			})
			if err != nil {
				return err
			}
			b, err := core.NewStatelessResource(ctx, "b", core.StatelessResourceArgs{
				Project: "orders", Environment: "dev",
				Env: map[string]pulumi.StringInput{"SHARED_KEY": pulumi.String("2")},
			})
			if err != nil {
				return err
			}
			_, err = core.NewStatelessResource(ctx, "c", core.StatelessResourceArgs{
				Project: "orders", Environment: "dev",
				Links: []core.Resource{a, b},
			})
			return err
		}, pulumi.WithMocks("orders", "dev", &noopMocks{}))

		// Assert.
		if !errors.Is(err, core.ErrDuplicateLinkEnvKey) {
			t.Fatalf("expected ErrDuplicateLinkEnvKey, got %v", err)
		}
	})
}

// Private helpers ------------------------------------------------------------

type noopMocks struct{}

func (*noopMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	return a.Name + "_id", a.Inputs, nil
}

func (*noopMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) { return a.Args, nil }

func newLinkedTestStateful(ctx *pulumi.Context, grantedCh chan<- string) (*core.CustomStateful, error) {
	db, err := core.NewStatefulResource(ctx, "db", core.StatefulResourceArgs{
		Project:     "orders",
		Repo:        "cloudvoyant/orders",
		Environment: "preview-pr-7",
		Kind:        "postgres",
		BranchFrom:  "dev",
		Env:         map[string]pulumi.StringInput{"DB_HOST": pulumi.String("db.internal")},
		Secrets:     map[string]pulumi.StringInput{"DATABASE_URL": pulumi.String("postgres://u:p@db.internal/app")},
	})
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
