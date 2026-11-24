package core_test

import (
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

func TestAccountContract(t *testing.T) {
	t.Run("given an AccountResource registration and ScopeRef manifest lookups", func(t *testing.T) {
		t.Run("when InitAccount runs, then it populates org metadata distinct from ResourceMeta", func(t *testing.T) {
			// Arrange.
			var acc core.AccountResource

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				ta := &testAccount{}
				if _, e := core.InitAccount(ctx, "test:org:Account", "gcp-account", ta, &ta.AccountMeta, core.AccountSpec{
					Name: "gcp",
					Org:  "cloudvoyant",
				}); e != nil {
					return e
				}
				acc = ta
				return nil
			}, pulumi.WithMocks("platform", "platform", &noopMocks{}))

			// Assert.
			require.NoError(t, err)
			require.Equal(t, "cloudvoyant-gcp", acc.OpenBaseAccount().CanonicalName)
			require.Equal(t, core.TierAccount, acc.OpenBaseAccount().Tags[core.TagTier])
		})

		t.Run("when multiple accounts of the same kind exist, then unnamed ManifestAttr returns ErrAmbiguousResource while named lookup succeeds", func(t *testing.T) {
			// Arrange.
			namedCh := make(chan string, 1)

			// Act.
			errAmbig := pulumi.RunErr(func(ctx *pulumi.Context) error {
				ref, err := newMultiAccountLocalRef(ctx)
				if err != nil {
					return err
				}
				ctx.Export("proj", ref.ManifestAttr("gcp-account", nil, "gcpProject"))
				return nil
			}, pulumi.WithMocks("platform", "platform", &noopMocks{}))

			errNamed := pulumi.RunErr(func(ctx *pulumi.Context) error {
				ref, err := newMultiAccountLocalRef(ctx)
				if err != nil {
					return err
				}
				ref.ManifestAttr("gcp-account", []string{"gcp-prod"}, "gcpProject").ApplyT(func(v string) string {
					namedCh <- v
					return v
				})
				return nil
			}, pulumi.WithMocks("platform", "platform", &noopMocks{}))

			// Assert.
			require.ErrorIs(t, errAmbig, core.ErrAmbiguousResource)
			require.NoError(t, errNamed)
			require.Equal(t, "acme-prod", <-namedCh)
		})
	})
}

// Private helpers ------------------------------------------------------------

func newMultiAccountLocalRef(ctx *pulumi.Context) (*core.ScopeRef, error) {
	nonprod, err := core.NewCustomAccount(ctx, "gcp-nonprod", &core.CustomAccountArgs{
		Org:        "acme",
		Kind:       "gcp-account",
		Attributes: map[string]pulumi.StringInput{"gcpProject": pulumi.String("acme-nonprod")},
	})
	if err != nil {
		return nil, err
	}
	prod, err := core.NewCustomAccount(ctx, "gcp-prod", &core.CustomAccountArgs{
		Org:        "acme",
		Kind:       "gcp-account",
		Attributes: map[string]pulumi.StringInput{"gcpProject": pulumi.String("acme-prod")},
	})
	if err != nil {
		return nil, err
	}
	return core.NewLocalAccountScopeRef("", map[string]core.AccountResource{
		"gcp-nonprod": nonprod,
		"gcp-prod":    prod,
	}), nil
}

type testAccount struct {
	pulumi.ResourceState
	core.AccountMeta
}

type noopMocks struct{}

func (*noopMocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	return a.Name + "_id", a.Inputs, nil
}

func (*noopMocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) { return a.Args, nil }
