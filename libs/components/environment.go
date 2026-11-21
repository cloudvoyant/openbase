package components

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatelessResource = (*EnvironmentResource)(nil)
	_ ScopedAdder            = (*EnvironmentResource)(nil)
)

// Environment declares a deployable application environment (dev, stage, prod, or preview-*) in Premise's infra/env stack.
type Environment struct {
	Name                   string
	Project                string
	Repo                   string
	Environment            string
	Platform               *core.PlatformRef
	PlatformStackRef       string
	ProjectRef             *core.ProjectRef
	ProjectStackRef        string
	DisableProjectStackRef bool
	Brokers                []core.IdentityBroker
	Resources              []core.ScopedSpec
	SkipStackExports       bool
}

// EnvironmentResource holds per-environment resources and supports incremental openbase.Add calls.
type EnvironmentResource struct {
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

// NewEnvironment initializes an openbase.Environment scope on dev, stage, prod, or preview-* for openbase.Add registrations.
func NewEnvironment(ctx *pulumi.Context, spec Environment, opts ...pulumi.ResourceOption) (*EnvironmentResource, error) {
	return spec.Register(ctx, opts...)
}

// Register validates the non-shared environment guardrail, provisions any initial Resources, and exports openbaseManifest.
func (e Environment) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*EnvironmentResource, error) {
	e.Name = defaultStr(e.Name, "env")
	res := &EnvironmentResource{
		Resources:   map[string]core.Resource{},
		ctx:         ctx,
		skipExports: e.SkipStackExports,
		exported:    map[string]bool{},
	}
	meta, err := core.InitStateless(ctx, "openbase:components:Environment", "openbase-environment", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        e.Name,
		Project:     e.Project,
		Repo:        e.Repo,
		Environment: e.Environment,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if meta.Environment == core.EnvShared {
		return nil, fmt.Errorf("%w: openbase.Environment is per-environment; declare openbase.Project in infra/shared for the shared stack", core.ErrInvalidEnvironment)
	}
	if err := initEnvironmentScope(ctx, meta, e, res); err != nil {
		return nil, err
	}
	if err := registerInitialEnvSpecs(ctx, e.Resources, res); err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
	})
}

// Project returns the parent ProjectRef, lazily binding a StackReference when backed by a remote shared stack.
func (r *EnvironmentResource) Project() *core.ProjectRef {
	if r == nil {
		return nil
	}
	ref := r.scope.ProjectRef
	if ref == nil || ref.StackReference() != nil || ref.StackName() == "" {
		return ref
	}
	sr, err := pulumi.NewStackReference(r.ctx, r.scope.Project+"-env-project-ref", &pulumi.StackReferenceArgs{
		Name: pulumi.String(ref.StackName()),
	})
	if err != nil {
		return ref
	}
	bound := &core.ProjectRef{
		ScopeRef: core.NewStackScopeRef(ref.StackName(), sr),
		Project:  ref.Project,
		Platform: ref.Platform,
	}
	r.scope.ProjectRef = bound
	return bound
}

// Platform returns the parent PlatformRef associated with this Environment, if configured.
func (r *EnvironmentResource) Platform() *core.PlatformRef {
	if r == nil {
		return nil
	}
	return r.scope.Platform
}

// Resource returns a provisioned environment Resource by logical name, or nil when absent.
func (r *EnvironmentResource) Resource(name string) core.Resource {
	if r == nil {
		return nil
	}
	return r.Resources[name]
}

// Private helpers ------------------------------------------------------------

func (r *EnvironmentResource) pulumiContext() *pulumi.Context { return r.ctx }

func (r *EnvironmentResource) registrationScope() core.Scope { return r.scope }

func (r *EnvironmentResource) recordResource(item core.Resource) {
	meta := item.OpenBaseMeta()
	desc := item.Link()
	r.Items = append(r.Items, item)
	r.Resources[meta.Name] = item
	r.descriptors = append(r.descriptors, desc)
	exportDescriptorOutputs(r.ctx, r.skipExports, r.exported, desc)
	exportScopeManifest(r.ctx, r.skipExports, r.scope.Org, r.scope.Project, r.scope.PlatformStackName(), r.descriptors)
}

func initEnvironmentScope(ctx *pulumi.Context, meta core.ResourceMeta, spec Environment, res *EnvironmentResource) error {
	org := core.OrgScope(ctx.Organization())
	platRef, err := resolveEnvPlatformRef(ctx, spec)
	if err != nil {
		return err
	}
	if platRef != nil && platRef.Org != "" {
		org = platRef.Org
	}
	projRef := resolveEnvProjectRef(ctx, meta.Project, platRef, spec)
	res.scope = core.Scope{
		Org:        org,
		Project:    meta.Project,
		Repo:       meta.Repo,
		Env:        meta.Environment,
		Platform:   platRef,
		ProjectRef: projRef,
		Brokers:    spec.Brokers,
	}
	exportScopeManifest(ctx, res.skipExports, org, meta.Project, res.scope.PlatformStackName(), nil)
	return nil
}

func resolveEnvPlatformRef(ctx *pulumi.Context, spec Environment) (*core.PlatformRef, error) {
	if spec.Platform != nil {
		return spec.Platform, nil
	}
	if spec.ProjectRef != nil && spec.ProjectRef.Platform != nil {
		return spec.ProjectRef.Platform, nil
	}
	if spec.PlatformStackRef == "" {
		return nil, nil
	}
	return LookupPlatform(ctx, spec.PlatformStackRef)
}

func resolveEnvProjectRef(
	ctx *pulumi.Context,
	project string,
	platRef *core.PlatformRef,
	spec Environment,
) *core.ProjectRef {
	if spec.ProjectRef != nil {
		return spec.ProjectRef
	}
	if spec.DisableProjectStackRef {
		return nil
	}
	ref := spec.ProjectStackRef
	if ref == "" {
		stackOrg := defaultStr(ctx.Organization(), "organization")
		stackProj := defaultStr(ctx.Project(), project)
		ref = fmt.Sprintf("%s/%s-shared/shared", stackOrg, stackProj)
	}
	return &core.ProjectRef{
		ScopeRef: core.NewStackScopeRef(ref, nil),
		Project:  project,
		Platform: platRef,
	}
}

func registerInitialEnvSpecs(ctx *pulumi.Context, specs []core.ScopedSpec, res *EnvironmentResource) error {
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
