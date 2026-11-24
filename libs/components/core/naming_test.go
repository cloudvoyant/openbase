package core_test

import (
	"strings"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

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
			require.NoError(t, errShort)
			require.NoError(t, errLongA)
			require.NoError(t, errLongB)
			require.NoError(t, errBucket)
			assertBoundedID(t, saShort, 6, 30)
			assertBoundedID(t, saLongA, 6, 30)
			require.Equal(t, saLongA, saLongB)
			assertBoundedID(t, bucket, 3, 63)
		})

		t.Run("when CanonicalName is called with EnvShared vs an environment, then shared omits env from naming", func(t *testing.T) {
			// Arrange / Act.
			sharedName, errShared := core.CanonicalName("storefront", core.EnvShared, "gcp")
			devName, errDev := core.CanonicalName("storefront", core.EnvDev, "gcp")
			sharedHost, errHost := core.HostLabel("storefront", core.EnvShared, "relay")

			// Assert.
			require.NoError(t, errShared)
			require.NoError(t, errDev)
			require.NoError(t, errHost)
			require.Equal(t, "storefront-gcp", sharedName)
			require.Equal(t, "storefront-dev-gcp", devName)
			require.Equal(t, "storefront-relay", sharedHost)
		})

		t.Run("when an identifier starts with a digit, then CanonicalName returns ErrInvalidIdentifier", func(t *testing.T) {
			// Arrange / Act.
			_, err := core.CanonicalName("123bad", core.EnvDev, "api")

			// Assert.
			require.ErrorIs(t, err, core.ErrInvalidIdentifier)
		})

		t.Run("when BranchFrom references an ephemeral, self, shared, or unknown tier, then NewCustomStateful rejects with ErrInvalidBranchSource", func(t *testing.T) {
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
					_, e := core.NewCustomStateful(ctx, "db", &core.CustomStatefulArgs{
						ProjectName: "orders",
						Environment: tc.target,
						BranchFrom:  tc.source,
					})
					return e
				}, pulumi.WithMocks("orders", tc.target, &noopMocks{}))
				require.ErrorIs(t, err, core.ErrInvalidBranchSource)
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

func assertBoundedID(t *testing.T, id string, minLen, maxLen int) {
	t.Helper()
	require.GreaterOrEqual(t, len(id), minLen)
	require.LessOrEqual(t, len(id), maxLen)
	require.False(t, strings.HasSuffix(id, "-"))
}
