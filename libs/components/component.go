// Package components exposes the root OpenBase Go Pulumi component library metadata.
//
// Import provider subpackages directly:
//   - github.com/cloudvoyant/openbase/libs/components/core
//   - github.com/cloudvoyant/openbase/libs/components/gcp
//   - github.com/cloudvoyant/openbase/libs/components/vercel
//   - github.com/cloudvoyant/openbase/libs/components/neon
//   - github.com/cloudvoyant/openbase/libs/components/kinde
//   - github.com/cloudvoyant/openbase/libs/components/synthetic
package components

import "github.com/cloudvoyant/openbase/libs/components/core"

// Public API -----------------------------------------------------------------

// LibraryName identifies the OpenBase Go Pulumi component package.
const LibraryName = "openbase-components"

// SupportedEnvironments returns the fixed environment tiers plus the preview prefix.
func SupportedEnvironments() []string {
	return []string{
		core.EnvShared.String(),
		core.EnvDev.String(),
		core.EnvStage.String(),
		core.EnvProd.String(),
		core.PreviewPrefix + "<slug>",
	}
}
