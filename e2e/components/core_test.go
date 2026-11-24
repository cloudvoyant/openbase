package e2e_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Public API -----------------------------------------------------------------

func TestCoreLifecycle(t *testing.T) {
	t.Run("given a Pulumi program declaring linked core.CustomStateful and core.CustomStateless specs", func(t *testing.T) {
		// Arrange.
		h := NewLiveHarness(t, "core-lifecycle", "openbase-core-lifecycle-fixture", "")
		h.InitStack(t, "preview-pr-42")
		h.InitStack(t, "dev")

		t.Run("when previewing a branch", func(t *testing.T) {
			t.Run("then an invalid environment override rejects during preview without creating state", func(t *testing.T) {
				// Arrange / Act.
				failOut := h.PulumiFail(t, []string{"OPENBASE_TEST_ENV_OVERRIDE=staging"}, "preview", "--stack", "preview-pr-42")

				// Assert.
				require.Contains(t, failOut, "invalid environment")
			})

			t.Run("then a valid preview branch plans without errors", func(t *testing.T) {
				// Arrange / Act.
				planOut := h.Pulumi(t, nil, "preview", "--stack", "preview-pr-42")
				planFile := h.WriteArtifact(t, "preview-branch-plan.txt", planOut)

				// Assert.
				assertNonEmptyFile(t, planFile)
			})
		})

		t.Run("when previewing on main", func(t *testing.T) {
			t.Run("then branching a persistent stack from an ephemeral preview stack rejects with ErrInvalidBranchSource", func(t *testing.T) {
				// Arrange / Act.
				failOut := h.PulumiFail(t, []string{"OPENBASE_TEST_BRANCH_FROM=preview-pr-42"}, "preview", "--stack", "dev")

				// Assert.
				require.Contains(t, failOut, "invalid branchFrom")
			})

			t.Run("then a persistent dev stack with no BranchFrom plans cleanly", func(t *testing.T) {
				// Arrange / Act.
				planOut := h.Pulumi(t, []string{"OPENBASE_TEST_BRANCH_FROM=prod"}, "preview", "--stack", "dev")

				// Assert.
				require.Contains(t, planOut, "openbase:core:StatefulResource")
			})
		})

		t.Run("when applying a branch", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, nil, "up", "--stack", "preview-pr-42", "--yes", "--non-interactive", "--skip-preview")
			outputs := map[string]string{
				"ledgerCanonicalName":  h.Output(t, "preview-pr-42", "ledgerCanonicalName"),
				"ledgerBranchSource":   h.Output(t, "preview-pr-42", "ledgerBranchSource"),
				"workerCanonicalName":  h.Output(t, "preview-pr-42", "workerCanonicalName"),
				"workerLinkedEndpoint": h.Output(t, "preview-pr-42", "workerLinkedEndpoint"),
			}
			snapshotFile := h.WriteJSONArtifact(t, "branch-stack-outputs.json", outputs)

			// Assert.
			t.Run("then it exports canonical names, branch metadata, and linked environment outputs", func(t *testing.T) {
				require.Equal(t, "storefront-preview-pr-42-ledger", outputs["ledgerCanonicalName"])
				require.Equal(t, "dev", outputs["ledgerBranchSource"])
				require.Equal(t, "storefront-preview-pr-42-worker", outputs["workerCanonicalName"])
				require.Equal(t, "https://ledger.storefront.internal", outputs["workerLinkedEndpoint"])
			})

			t.Run("then it writes the CLI transcript and stack output snapshot under e2e/outputs/components", func(t *testing.T) {
				assertNonEmptyFile(t, filepath.Join(h.OutputDir, "commands.log"))
				assertNonEmptyFile(t, snapshotFile)
			})
		})

		t.Run("when applying on main, then it provisions the persistent dev stack with canonical names", func(t *testing.T) {
			// Arrange / Act.
			h.Pulumi(t, []string{"OPENBASE_TEST_BRANCH_FROM=prod"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

			// Assert.
			require.Equal(t, "storefront-dev-ledger", h.Output(t, "dev", "ledgerCanonicalName"))
			require.Equal(t, "storefront-dev-worker", h.Output(t, "dev", "workerCanonicalName"))
		})
	})
}

// Private helpers ------------------------------------------------------------

func assertNonEmptyFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Positive(t, info.Size())
}
