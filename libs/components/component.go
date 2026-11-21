// Package components provides shared cross-stack lookup helpers and environment conventions for OpenBase Pulumi Go components.
package components

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

// LibraryName identifies the OpenBase Go Pulumi component package.
const LibraryName = "openbase-components"

// LookupPlatform creates a Pulumi StackReference to an organization platform stack (defaulting to "<org>/platform/platform").
func LookupPlatform(ctx *pulumi.Context, stackRef ...string) (*core.ScopeRef, error) {
	stackOrg := defaultStr(ctx.Organization(), "organization")
	ref := fmt.Sprintf("%s/platform/platform", stackOrg)
	if len(stackRef) > 0 && stackRef[0] != "" {
		ref = stackRef[0]
	}
	sr, err := pulumi.NewStackReference(ctx, "openbase-platform-ref", &pulumi.StackReferenceArgs{
		Name: pulumi.String(ref),
	})
	if err != nil {
		return nil, err
	}
	return core.NewStackScopeRef(ref, sr), nil
}

// LookupShared creates a Pulumi StackReference to a project's shared stack (defaulting to "<org>/<project>-shared/shared").
func LookupShared(ctx *pulumi.Context, stackRef ...string) (*core.ScopeRef, error) {
	stackOrg := defaultStr(ctx.Organization(), "organization")
	proj := core.ResolveProject(ctx, "")
	ref := fmt.Sprintf("%s/%s-shared/shared", stackOrg, proj)
	if len(stackRef) > 0 && stackRef[0] != "" {
		ref = stackRef[0]
	}
	sr, err := pulumi.NewStackReference(ctx, "openbase-shared-ref", &pulumi.StackReferenceArgs{
		Name: pulumi.String(ref),
	})
	if err != nil {
		return nil, err
	}
	return core.NewStackScopeRef(ref, sr), nil
}

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

// Private helpers ------------------------------------------------------------

func defaultStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}
