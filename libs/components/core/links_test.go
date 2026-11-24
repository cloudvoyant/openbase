package core_test

import (
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

func TestLinksAndTags(t *testing.T) {
	t.Run("given linked core.CustomStateful and core.CustomStateless specs", func(t *testing.T) {
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
				api, err := core.NewCustomStateless(ctx, "api", &core.CustomStatelessArgs{
					ProjectName: "orders",
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
			require.NoError(t, err)
			require.Equal(t, "pr-7", stateMeta.Tags[core.TagPreviewID])
			require.Equal(t, "true", stateMeta.Tags[core.TagStateful])
			require.Equal(t, "false", appMeta.Tags[core.TagStateful])
			require.NotNil(t, gcpLabels["openbase-preview-id"])
			require.Equal(t, "orders-preview-pr-7-api", <-grantedCh)
			require.Equal(t, "orders-preview-pr-7-api", <-grantedCh)
			require.Equal(t, "db.internal", <-attrCh)
		})
	})
}

// Private helpers ------------------------------------------------------------

func newLinkedTestStateful(ctx *pulumi.Context, name string, grantedCh chan<- string) (*core.CustomStateful, error) {
	return core.NewCustomStateful(ctx, name, &core.CustomStatefulArgs{
		ProjectName: "orders",
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
