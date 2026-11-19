// Package components provides the unified OpenBase Platform, Project, and Environment scopes plus typed cross-stack lookup and resource registration.
package components

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

// LibraryName identifies the OpenBase Go Pulumi component package.
const LibraryName = "openbase-components"

// ScopedAdder is implemented by *ProjectResource and *EnvironmentResource to support typed openbase.Add registrations.
type ScopedAdder interface {
	pulumi.Resource
	pulumiContext() *pulumi.Context
	registrationScope() core.Scope
	recordResource(res core.Resource)
}

// Add registers a typed ScopedSpec inside a ProjectResource or EnvironmentResource, returning the concrete handle R.
func Add[S core.ScopedSpecOf[R], R core.Resource](
	target ScopedAdder,
	spec S,
	opts ...pulumi.ResourceOption,
) (R, error) {
	ctx := target.pulumiContext()
	childOpts := append([]pulumi.ResourceOption{pulumi.Parent(target)}, opts...)
	res, err := spec.RegisterTypedInScope(ctx, target.registrationScope(), childOpts...)
	if err != nil {
		var zero R
		return zero, err
	}
	target.recordResource(res)
	return res, nil
}

// FindPlatform returns the first registered PlatformResource matching type R inside plat, or the zero value when absent.
func FindPlatform[R core.PlatformResource](plat *PlatformResource) R {
	var zero R
	if plat == nil {
		return zero
	}
	for _, item := range plat.Items {
		if typed, ok := item.(R); ok {
			return typed
		}
	}
	return zero
}

// FindResource returns the first registered Resource matching type R inside a ProjectResource or EnvironmentResource.
func FindResource[R core.Resource](items []core.Resource) R {
	var zero R
	for _, item := range items {
		if typed, ok := item.(R); ok {
			return typed
		}
	}
	return zero
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

func exportDescriptorOutputs(ctx *pulumi.Context, skip bool, desc core.LinkDescriptor) {
	if skip {
		return
	}
	for k, v := range desc.StackExports {
		ctx.Export(k, v)
	}
}

func exportScopeManifest(
	ctx *pulumi.Context,
	skip bool,
	org, project, platformStack string,
	descriptors []core.LinkDescriptor,
) {
	if skip {
		return
	}
	ctx.Export(core.ManifestOutputKey, core.BuildManifestMap(org, project, platformStack, descriptors))
}
