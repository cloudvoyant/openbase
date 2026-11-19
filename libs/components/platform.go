package components

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.PlatformResource = (*PlatformResource)(nil)

// Platform declares organization-wide foundations provisioned once per organization on the central platform stack.
type Platform struct {
	Name             string
	Org              string
	Resources        []core.PlatformSpecItem
	SkipStackExports bool
}

// PlatformResource holds provisioned organization-level provider foundations and exports openbaseManifest.
type PlatformResource struct {
	pulumi.ResourceState
	core.PlatformMeta

	Org       string
	Items     []core.PlatformResource
	Resources map[string]core.PlatformResource
}

// Register provisions every PlatformSpecItem in Resources, exports stack outputs and openbaseManifest, and returns PlatformResource.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	p.Name = defaultStr(p.Name, "platform")
	orgScope := core.OrgScope(p.Org, ctx.Organization())
	res := &PlatformResource{
		Org:       orgScope,
		Resources: map[string]core.PlatformResource{},
	}
	info, err := core.InitPlatform(ctx, "openbase:components:Platform", "openbase-platform", res, &res.PlatformMeta, core.PlatformSpec{
		Name: p.Name,
		Org:  orgScope,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := registerPlatformItems(ctx, orgScope, p, res); err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(info.CanonicalName),
	})
}

// Ref returns an in-memory PlatformRef suitable for single-process Project or Environment composition.
func (r *PlatformResource) Ref() *core.PlatformRef {
	if r == nil {
		return nil
	}
	return &core.PlatformRef{
		ScopeRef: core.NewLocalPlatformScopeRef("", r.Resources),
		Org:      r.Org,
	}
}

// Resource returns a provisioned PlatformResource by logical name, or nil when absent.
func (r *PlatformResource) Resource(name string) core.PlatformResource {
	if r == nil {
		return nil
	}
	return r.Resources[name]
}

// LookupPlatform creates a Pulumi StackReference to an organization Platform stack (defaulting to "<org>/platform/platform").
func LookupPlatform(ctx *pulumi.Context, stackRef ...string) (*core.PlatformRef, error) {
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
	return &core.PlatformRef{
		ScopeRef: core.NewStackScopeRef(ref, sr),
		Org:      core.OrgScope(ctx.Organization()),
	}, nil
}

// Private helpers ------------------------------------------------------------

func registerPlatformItems(ctx *pulumi.Context, orgScope string, spec Platform, res *PlatformResource) error {
	scope := core.Scope{Org: orgScope}
	var descriptors []core.LinkDescriptor
	for _, itemSpec := range spec.Resources {
		if itemSpec == nil {
			continue
		}
		item, err := itemSpec.RegisterPlatformInScope(ctx, scope, pulumi.Parent(res))
		if err != nil {
			return err
		}
		info := item.OpenBasePlatform()
		desc := item.Link()
		res.Items = append(res.Items, item)
		res.Resources[info.Name] = item
		descriptors = append(descriptors, desc)
		exportDescriptorOutputs(ctx, spec.SkipStackExports, desc)
	}
	exportScopeManifest(ctx, spec.SkipStackExports, orgScope, "", "", descriptors)
	return nil
}
