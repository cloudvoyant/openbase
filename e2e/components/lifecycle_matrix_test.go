package e2e_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Private helpers ------------------------------------------------------------

type resourceLifecycleSpec struct {
	Fixture       string
	PulumiProject string
	Backend       string
	Env           []string
	HasShared     bool
	TargetStack   string
	PlanToken     string
	UpdateEnv     []string
	AssertApplied func(t *testing.T, h *LiveStackHarness, stack string)
	AssertUpdated func(t *testing.T, h *LiveStackHarness, stack string)
	AssertAbsent  func(t *testing.T, h *LiveStackHarness)
}

func runResourceLifecycleMatrix(t *testing.T, spec resourceLifecycleSpec) {
	t.Helper()
	stack := spec.TargetStack
	if stack == "" {
		stack = "dev"
	}
	h := NewLiveHarness(t, spec.Fixture, spec.PulumiProject, spec.Backend, spec.Env...)
	if spec.HasShared {
		h.InitStack(t, "shared")
		h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
	}
	h.InitStack(t, stack)

	t.Run("given no infra exists", func(t *testing.T) {
		t.Run("when planning, then preview plans the expected resource without creating state", func(t *testing.T) {
			planOut := h.Pulumi(t, nil, "preview", "--stack", stack)
			h.WriteArtifact(t, "fresh-plan.txt", planOut)
			require.Contains(t, planOut, spec.PlanToken)
		})

		t.Run("when applying, then up provisions the resource and exports valid outputs", func(t *testing.T) {
			h.Pulumi(t, nil, "up", "--stack", stack, "--yes", "--non-interactive", "--skip-preview")
			if spec.AssertApplied != nil {
				spec.AssertApplied(t, h, stack)
			}
		})

		t.Run("when destroying, then destroy tears down the resource cleanly", func(t *testing.T) {
			h.Pulumi(t, nil, "destroy", "--stack", stack, "--yes", "--non-interactive", "--skip-preview")
			if spec.AssertAbsent != nil {
				spec.AssertAbsent(t, h)
			}
		})
	})

	t.Run("given prior infra exists", func(t *testing.T) {
		h.Pulumi(t, nil, "up", "--stack", stack, "--yes", "--non-interactive", "--skip-preview")

		t.Run("when planning, then preview plans the update against existing state", func(t *testing.T) {
			planOut := h.Pulumi(t, spec.UpdateEnv, "preview", "--stack", stack)
			h.WriteArtifact(t, "update-plan.txt", planOut)
			require.Contains(t, planOut, spec.PlanToken)
		})

		t.Run("when applying, then up updates the existing resource in place", func(t *testing.T) {
			h.Pulumi(t, spec.UpdateEnv, "up", "--stack", stack, "--yes", "--non-interactive", "--skip-preview")
			if spec.AssertUpdated != nil {
				spec.AssertUpdated(t, h, stack)
			}
		})

		t.Run("when destroying, then destroy removes all resources cleanly", func(t *testing.T) {
			h.Pulumi(t, nil, "destroy", "--stack", stack, "--yes", "--non-interactive", "--skip-preview")
			if spec.AssertAbsent != nil {
				spec.AssertAbsent(t, h)
			}
		})
	})
}
