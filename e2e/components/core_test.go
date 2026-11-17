package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestCoreLifecycle(t *testing.T) {
	t.Run("given a standalone Pulumi program declaring linked core.Stateful and core.Stateless specs", func(t *testing.T) {
		// Arrange.
		h := NewLiveHarness(t, "core-lifecycle", "openbase-core-lifecycle-fixture", "file://"+filepath.ToSlash(t.TempDir()))
		h.InitStack(t, "preview-pr-42")
		h.InitStack(t, "dev")

		t.Run("when previewing with an invalid environment name, then it rejects during preview without creating state", func(t *testing.T) {
			// Arrange.
			env := []string{"OPENBASE_TEST_ENV_OVERRIDE=staging"}

			// Act.
			failOut := h.PulumiFail(t, env, "preview", "--stack", "preview-pr-42")

			// Assert.
			if !strings.Contains(failOut, "invalid environment") {
				t.Fatalf("expected invalid environment error, got:\n%s", failOut)
			}
		})

		t.Run("when previewing a persistent stack branched from an ephemeral preview stack, then it rejects InvalidBranchSource", func(t *testing.T) {
			// Arrange.
			env := []string{"OPENBASE_TEST_BRANCH_FROM=preview-pr-42"}

			// Act.
			failOut := h.PulumiFail(t, env, "preview", "--stack", "dev")

			// Assert.
			if !strings.Contains(failOut, "invalid branchFrom") {
				t.Fatalf("expected invalid branchFrom error, got:\n%s", failOut)
			}
		})

		t.Run("when applying preview-pr-42 branched from dev", func(t *testing.T) {
			// Arrange / Act.
			planOut := h.Pulumi(t, nil, "preview", "--stack", "preview-pr-42")
			h.Pulumi(t, nil, "up", "--stack", "preview-pr-42", "--yes", "--non-interactive", "--skip-preview")
			outputs := map[string]string{
				"ledgerCanonicalName":  h.Output(t, "preview-pr-42", "ledgerCanonicalName"),
				"ledgerBranchSource":   h.Output(t, "preview-pr-42", "ledgerBranchSource"),
				"workerCanonicalName":  h.Output(t, "preview-pr-42", "workerCanonicalName"),
				"workerLinkedEndpoint": h.Output(t, "preview-pr-42", "workerLinkedEndpoint"),
			}
			planFile := h.WriteArtifact(t, "preview-plan.txt", planOut)
			snapshotFile := h.WriteJSONArtifact(t, "stack-outputs.json", outputs)

			// Assert.
			t.Run("then it exports canonical names, branch metadata, and linked environment outputs", func(t *testing.T) {
				assertEqualStr(t, "ledgerCanonicalName", outputs["ledgerCanonicalName"], "storefront-preview-pr-42-ledger")
				assertEqualStr(t, "ledgerBranchSource", outputs["ledgerBranchSource"], "dev")
				assertEqualStr(t, "workerCanonicalName", outputs["workerCanonicalName"], "storefront-preview-pr-42-worker")
				assertEqualStr(t, "workerLinkedEndpoint", outputs["workerLinkedEndpoint"], "https://ledger.storefront.internal")
			})

			t.Run("then it writes the CLI transcript and stack output snapshot under e2e/outputs/components", func(t *testing.T) {
				assertNonEmptyFile(t, filepath.Join(h.OutputDir, "commands.log"))
				assertNonEmptyFile(t, planFile)
				assertNonEmptyFile(t, snapshotFile)
			})
		})
	})
}

// Private helpers ------------------------------------------------------------

func assertNonEmptyFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty artifact file at %s (err=%v)", path, err)
	}
}
