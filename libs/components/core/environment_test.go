package core_test

import (
	"testing"

	"github.com/stretchr/testify/require"

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
				require.NoError(t, err)
				require.Equal(t, raw, env.String())
			}
		})

		t.Run("when parsing malformed or unsupported tiers, then ParseEnvironment returns ErrInvalidEnvironment", func(t *testing.T) {
			// Arrange.
			invalid := []string{"", "project", "platform", "qa", "production", "staging", "preview-", "preview--bad-", "Preview-1", "preview-UPPER"}

			// Act / Assert.
			for _, raw := range invalid {
				_, err := core.ParseEnvironment(raw)
				require.ErrorIs(t, err, core.ErrInvalidEnvironment)
			}
		})

		t.Run("when inspecting lifecycle predicates", func(t *testing.T) {
			// Arrange.
			preview := core.Environment("preview-feat-auth-99")

			// Act / Assert.
			t.Run("then prod and stage are protected and persistent while dev is unprotected", func(t *testing.T) {
				require.True(t, core.EnvProd.IsProtected())
				require.True(t, core.EnvStage.IsProtected())
				require.False(t, core.EnvDev.IsProtected())
				require.True(t, core.EnvDev.IsPersistent())
				require.False(t, preview.IsPersistent())
			})

			t.Run("then preview-<slug> is ephemeral and exposes its preview-id", func(t *testing.T) {
				require.True(t, preview.IsEphemeral())
				require.Equal(t, "feat-auth-99", preview.PreviewID())
				require.Empty(t, core.EnvDev.PreviewID())
			})
		})
	})
}
