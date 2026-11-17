package e2e_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestSyntheticConsumerFixture(t *testing.T) {
	t.Run("when applying and previewing a standalone Go consumer fixture offline", func(t *testing.T) {
		// Arrange.
		h := NewLiveHarness(t, "synthetic", "openbase-synthetic-fixture", "file://"+filepath.ToSlash(t.TempDir()))
		h.InitStack(t, "preview-pr-42")
		cleaned := false
		h.OnCleanupVerify(func(_ *testing.T) { cleaned = true })

		// Act.
		plan := h.Pulumi(t, nil, "preview", "--stack", "preview-pr-42", "--non-interactive")
		h.Pulumi(t, nil, "up", "--stack", "preview-pr-42", "--yes", "--non-interactive", "--skip-preview")
		bucket := h.Output(t, "preview-pr-42", "bucket")
		fallback := h.Output(t, "preview-pr-42", "fallbackBucket")
		badErr := h.PulumiFail(
			t,
			[]string{"OPENBASE_TEST_ENV_OVERRIDE=invalid_env"},
			"preview",
			"--stack",
			"preview-pr-42",
			"--non-interactive",
		)

		// Assert.
		t.Run("it plans and exports synthetic Storage and Service outputs", func(t *testing.T) {
			if !strings.Contains(plan, "openbase:synthetic:Storage") || !strings.Contains(plan, "openbase:synthetic:Service") {
				t.Fatalf("unexpected preview output:\n%s", plan)
			}
			if bucket != "storefront-preview-pr-42-uploads" || fallback != "storefront-dev-uploads" {
				t.Fatalf("unexpected outputs: bucket=%q fallback=%q", bucket, fallback)
			}
			if cleaned {
				t.Fatal("cleanup ran prematurely")
			}
		})
		t.Run("it fails fast during preview when an invalid environment is supplied", func(t *testing.T) {
			if !strings.Contains(badErr, "invalid environment") {
				t.Fatalf("expected invalid environment failure, got:\n%s", badErr)
			}
		})
	})
}
