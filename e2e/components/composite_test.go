package e2e_test

import "testing"

// Public API -----------------------------------------------------------------

// TestCompositeStacks exercises multi-provider composite stacks and cross-cloud linking in a single matrix.
func TestCompositeStacks(t *testing.T) {
	matrix := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "vercel-neon-kinde-app", run: runVercelNeonKindeComposite},
		{name: "gcp-neon-kinde-app", run: runGCPNeonKindeComposite},
		{name: "links-fullstack", run: runLinksFullstackComposite},
	}
	for _, tc := range matrix {
		t.Run("given composite stack "+tc.name, tc.run)
	}
}
