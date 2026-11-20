package components

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatelessResource = (*ProjectResource)(nil)
	_ ScopedAdder            = (*ProjectResource)(nil)
)

// Project declares cross-environment application assets in Premise's infra/shared stack (Environment: shared).
type Project struct {
	Name             string
	Project          string
	Repo             string
	Platform         *core.PlatformRef
	PlatformStackRef string
	Resources        []core.ScopedSpec
	SkipStackExports bool
}

// ProjectResource holds shared project resources and supports incremental openbase.Add calls.
type ProjectResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	Items     []core.Resource
	Resources map[string]core.Resource

	ctx         *pulumi.Context
	scope       core.Scope
	skipExports bool
	exported    map[string]bool
	descriptors []core.LinkDescriptor
}

// NewProject initializes an openbase.Project scope on the "shared" stack for incremental openbase.Add registrations.
func NewProject(ctx *pulumi.Context, spec Project, opts ...pulumi.ResourceOption) (*ProjectResource, error) {
	return spec.Register(ctx, opts...)
}

// Register validates the shared stack guardrail, provisions any initial Resources, and exports openbaseManifest.
func (p Project) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*ProjectResource, error) {
	p.Name = defaultStr(p.Name, "project")
	res := &ProjectResource{
		Resources:   map[string]core.Resource{},
		ctx:         ctx,
		skipExports: p.SkipStackExports,
		exported:    map[string]bool{},
	}
	meta, err := core.InitStateless(ctx, "openbase:components:Project", "openbase-project", res, &res.StatelessMeta, core.StatelessSpec{
		Name:    p.Name,
		Project: p.Project,
		Repo:    p.Repo,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if meta.Environment != core.EnvShared {
		return nil, fmt.Errorf("%w: openbase.Project requires the shared stack, got %q", core.ErrInvalidEnvironment, meta.Environment)
	}
	if err := initProjectScope(ctx, meta, p, res); err != nil {
		return nil, err
	}
	if err := registerInitialProjectSpecs(ctx, p.Resources, res); err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
	})
}

// Ref returns an in-memory ProjectRef suitable for single-process Environment composition.
func (r *ProjectResource) Ref() *core.ProjectRef {
	if r == nil {
		return nil
	}
	return &core.ProjectRef{
		ScopeRef: core.NewLocalResourceScopeRef("", r.Resources),
		Project:  r.scope.Project,
		Platform: r.scope.Platform,
	}
}

// Platform returns the parent PlatformRef associated with this Project, if configured.
func (r *ProjectResource) Platform() *core.PlatformRef {
	if r == nil {
		return nil
	}
	return r.scope.Platform
}

// Resource returns a provisioned shared Resource by logical name, or nil when absent.
func (r *ProjectResource) Resource(name string) core.Resource {
	if r == nil {
		return nil
	}
	return r.Resources[name]
}

// LookupProject creates a Pulumi StackReference to a project's shared stack (defaulting to "<org>/<project>-shared/shared").
func LookupProject(ctx *pulumi.Context, project string, stackRef ...string) (*core.ProjectRef, error) {
	stackOrg := defaultStr(ctx.Organization(), "organization")
	proj := defaultStr(project, ctx.Project())
	ref := fmt.Sprintf("%s/%s-shared/shared", stackOrg, defaultStr(ctx.Project(), proj))
	if len(stackRef) > 0 && stackRef[0] != "" {
		ref = stackRef[0]
	}
	sr, err := pulumi.NewStackReference(ctx, proj+"-shared-ref", &pulumi.StackReferenceArgs{
		Name: pulumi.String(ref),
	})
	if err != nil {
		return nil, err
	}
	return &core.ProjectRef{
		ScopeRef: core.NewStackScopeRef(ref, sr),
		Project:  proj,
	}, nil
}

// Private helpers ------------------------------------------------------------

func (r *ProjectResource) pulumiContext() *pulumi.Context { return r.ctx }

func (r *ProjectResource) registrationScope() core.Scope { return r.scope }

func (r *ProjectResource) recordResource(item core.Resource) {
	meta := item.OpenBaseMeta()
	desc := item.Link()
	r.Items = append(r.Items, item)
	r.Resources[meta.Name] = item
	r.descriptors = append(r.descriptors, desc)
	exportDescriptorOutputs(r.ctx, r.skipExports, r.exported, desc)
	exportScopeManifest(r.ctx, r.skipExports, r.scope.Org, r.scope.Project, r.scope.PlatformStackName(), r.descriptors)
}

func initProjectScope(ctx *pulumi.Context, meta core.ResourceMeta, spec Project, res *ProjectResource) error {
	platRef := spec.Platform
	if platRef == nil && spec.PlatformStackRef != "" {
		lookedUp, err := LookupPlatform(ctx, spec.PlatformStackRef)
		if err != nil {
			return err
		}
		platRef = lookedUp
	}
	org := core.OrgScope(ctx.Organization())
	if platRef != nil && platRef.Org != "" {
		org = platRef.Org
	}
	res.scope = core.Scope{
		Org:      org,
		Project:  meta.Project,
		Repo:     meta.Repo,
		Env:      core.EnvShared,
		Platform: platRef,
	}
	exportScopeManifest(ctx, res.skipExports, org, meta.Project, res.scope.PlatformStackName(), nil)
	return nil
}

func registerInitialProjectSpecs(ctx *pulumi.Context, specs []core.ScopedSpec, res *ProjectResource) error {
	for _, s := range specs {
		if s == nil {
			continue
		}
		item, err := s.RegisterInScope(ctx, res.scope, pulumi.Parent(res))
		if err != nil {
			return err
		}
		res.recordResource(item)
	}
	return nil
}
