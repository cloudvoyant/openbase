package components_test

import (
	"slices"
	"testing"

	"github.com/cloudvoyant/openbase/libs/components"
)

// Public API -----------------------------------------------------------------

func TestSupportedEnvironments(t *testing.T) {
	t.Run("when listing supported environments", func(t *testing.T) {
		// Arrange.
		want := []string{"shared", "dev", "stage", "prod", "preview-<slug>"}

		// Act.
		got := components.SupportedEnvironments()

		// Assert.
		t.Run("it includes shared, persistent tiers, and preview-<slug>", func(t *testing.T) {
			if !slices.Equal(got, want) {
				t.Fatalf("SupportedEnvironments() = %v, want %v", got, want)
			}
		})
	})
}
