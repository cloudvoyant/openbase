package core

import "github.com/pulumi/pulumi/sdk/v3/go/pulumi"

// Public API -----------------------------------------------------------------

// ResourceMeta captures validated lifecycle metadata for any OpenBase component.
type ResourceMeta struct {
	Name          string
	CanonicalName string
	Project       string
	Repo          string
	Kind          string
	Environment   Environment
	Stateful      bool
	Ephemeral     bool
	Protected     bool
	Backup        bool
	BranchFrom    Environment
	Tags          map[string]string
}

// Resource is satisfied by every OpenBase component handle across Platform, Project, and Environment tiers.
type Resource interface {
	pulumi.Resource
	Link() LinkDescriptor
}

// EnvironmentResource marks per-environment component handles deployed on dev, stage, prod, or preview-<slug>.
type EnvironmentResource interface {
	Resource
	OpenBaseMeta() ResourceMeta
}

// StatelessResource marks ephemeral/replaceable environment-level compute or routing component handles.
type StatelessResource interface {
	EnvironmentResource
}

// StatefulResource marks environment-level component handles that hold persistent data and support BranchFrom.
type StatefulResource interface {
	EnvironmentResource
	BranchSource() string
}

// StatelessSpec holds shared lifecycle inputs for registering a StatelessResource.
type StatelessSpec struct {
	Name        string
	Project     string
	Repo        string
	Environment string
	Kind        string
	Links       []Resource
	Env         map[string]pulumi.StringInput
	Secrets     map[string]pulumi.StringInput
}

// StatefulSpec holds shared lifecycle, protection, and BranchFrom inputs for registering a StatefulResource.
type StatefulSpec struct {
	Name        string
	Project     string
	Repo        string
	Environment string
	Kind        string
	BranchFrom  string
	Backup      *bool
	Links       []Resource
	Env         map[string]pulumi.StringInput
	Secrets     map[string]pulumi.StringInput
}

// StatelessMeta stores metadata and Links on structs that directly embed pulumi.ResourceState.
type StatelessMeta struct {
	meta ResourceMeta
	link LinkDescriptor
}

// OpenBaseMeta returns the validated metadata for the stateless component.
func (b *StatelessMeta) OpenBaseMeta() ResourceMeta { return b.meta }

// Link returns the LinkDescriptor exported by the stateless component.
func (b *StatelessMeta) Link() LinkDescriptor { return b.link }

// SetLink sets the LinkDescriptor exported by the stateless component.
func (b *StatelessMeta) SetLink(desc LinkDescriptor) { b.link = desc }

// StatefulMeta stores metadata, BranchSource, and Links on structs that directly embed pulumi.ResourceState.
type StatefulMeta struct {
	meta ResourceMeta
	link LinkDescriptor
}

// OpenBaseMeta returns the validated metadata for the stateful component.
func (b *StatefulMeta) OpenBaseMeta() ResourceMeta { return b.meta }

// Link returns the LinkDescriptor exported by the stateful component.
func (b *StatefulMeta) Link() LinkDescriptor { return b.link }

// SetLink sets the LinkDescriptor exported by the stateful component.
func (b *StatefulMeta) SetLink(desc LinkDescriptor) { b.link = desc }

// BranchSource returns the persistent source environment when branching, or "".
func (b *StatefulMeta) BranchSource() string { return b.meta.BranchFrom.String() }

// BuildStatelessMeta validates StatelessSpec and produces deterministic metadata.
func BuildStatelessMeta(ctx *pulumi.Context, spec StatelessSpec, defaultKind string) (ResourceMeta, error) {
	env, err := ResolveEnvironment(ctx, spec.Environment)
	if err != nil {
		return ResourceMeta{}, err
	}
	canonical, err := CanonicalName(spec.Project, env, spec.Name)
	if err != nil {
		return ResourceMeta{}, err
	}
	kind := spec.Kind
	if kind == "" {
		kind = defaultKind
	}
	meta := ResourceMeta{
		Name:          spec.Name,
		CanonicalName: canonical,
		Project:       spec.Project,
		Repo:          spec.Repo,
		Kind:          kind,
		Environment:   env,
		Stateful:      false,
		Ephemeral:     env.IsEphemeral(),
		Protected:     false,
	}
	meta.Tags = StandardTags(meta)
	return meta, nil
}

// BuildStatefulMeta validates StatefulSpec (including BranchFrom) and produces deterministic metadata.
func BuildStatefulMeta(ctx *pulumi.Context, spec StatefulSpec, defaultKind string) (ResourceMeta, error) {
	env, err := ResolveEnvironment(ctx, spec.Environment)
	if err != nil {
		return ResourceMeta{}, err
	}
	branchSrc, err := ValidateBranchSource(env, spec.BranchFrom)
	if err != nil {
		return ResourceMeta{}, err
	}
	canonical, err := CanonicalName(spec.Project, env, spec.Name)
	if err != nil {
		return ResourceMeta{}, err
	}
	kind := spec.Kind
	if kind == "" {
		kind = defaultKind
	}
	protected := env.IsProtected()
	backup := protected
	if spec.Backup != nil {
		backup = *spec.Backup
	}
	meta := ResourceMeta{
		Name:          spec.Name,
		CanonicalName: canonical,
		Project:       spec.Project,
		Repo:          spec.Repo,
		Kind:          kind,
		Environment:   env,
		Stateful:      true,
		Ephemeral:     env.IsEphemeral(),
		Protected:     protected,
		Backup:        backup,
		BranchFrom:    branchSrc,
	}
	meta.Tags = StandardTags(meta)
	return meta, nil
}

// InitStateless validates spec and registers a provider StatelessResource component.
func InitStateless(
	ctx *pulumi.Context,
	typeToken, defaultKind string,
	self pulumi.ComponentResource,
	holder *StatelessMeta,
	spec StatelessSpec,
	opts ...pulumi.ResourceOption,
) (ResourceMeta, error) {
	meta, err := BuildStatelessMeta(ctx, spec, defaultKind)
	if err != nil {
		return ResourceMeta{}, err
	}
	holder.meta = meta
	if err := ctx.RegisterComponentResource(typeToken, spec.Name, self, opts...); err != nil {
		return ResourceMeta{}, err
	}
	return meta, nil
}

// InitStateful validates spec and registers a provider StatefulResource component.
func InitStateful(
	ctx *pulumi.Context,
	typeToken, defaultKind string,
	self pulumi.ComponentResource,
	holder *StatefulMeta,
	spec StatefulSpec,
	opts ...pulumi.ResourceOption,
) (ResourceMeta, error) {
	meta, err := BuildStatefulMeta(ctx, spec, defaultKind)
	if err != nil {
		return ResourceMeta{}, err
	}
	holder.meta = meta
	if err := ctx.RegisterComponentResource(typeToken, spec.Name, self, opts...); err != nil {
		return ResourceMeta{}, err
	}
	return meta, nil
}

// ChildOptions returns pulumi.Parent(parent) plus Protect when meta.Protected is true.
func ChildOptions(parent pulumi.Resource, meta ResourceMeta, extra ...pulumi.ResourceOption) []pulumi.ResourceOption {
	opts := []pulumi.ResourceOption{pulumi.Parent(parent)}
	if meta.Stateful && meta.Protected {
		opts = append(opts, pulumi.Protect(true))
	}
	return append(opts, extra...)
}

// Stateless declares an escape-hatch stateless component wrapping custom Pulumi resources.
type Stateless struct {
	Name        string
	Project     string
	Repo        string
	Environment string
	Kind        string
	Links       []Resource
	Env         map[string]pulumi.StringInput
	Secrets     map[string]pulumi.StringInput
}

// CustomStatelessResource is the provisioned handle returned by Stateless.Register.
type CustomStatelessResource struct {
	pulumi.ResourceState
	StatelessMeta
}

// Register validates and provisions an escape-hatch StatelessResource with canonical tags and Links.
func (s Stateless) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*CustomStatelessResource, error) {
	res := &CustomStatelessResource{}
	spec := StatelessSpec(s)
	meta, err := InitStateless(ctx, "openbase:core:StatelessResource", "custom-stateless", res, &res.StatelessMeta, spec, opts...)
	if err != nil {
		return nil, err
	}
	desc, err := buildEscapeHatchLink(ctx, meta, s.Links, s.Env, s.Secrets)
	if err != nil {
		return nil, err
	}
	res.SetLink(desc)
	if err := ctx.RegisterResourceOutputs(res, pulumi.Map{"canonicalName": pulumi.String(meta.CanonicalName)}); err != nil {
		return nil, err
	}
	return res, nil
}

// Stateful declares an escape-hatch stateful component wrapping custom Pulumi resources.
type Stateful struct {
	Name        string
	Project     string
	Repo        string
	Environment string
	Kind        string
	BranchFrom  string
	Backup      *bool
	Links       []Resource
	Env         map[string]pulumi.StringInput
	Secrets     map[string]pulumi.StringInput
}

// CustomStatefulResource is the provisioned handle returned by Stateful.Register.
type CustomStatefulResource struct {
	pulumi.ResourceState
	StatefulMeta
}

// Register validates and provisions an escape-hatch StatefulResource with BranchFrom, tags, and Links.
func (s Stateful) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*CustomStatefulResource, error) {
	res := &CustomStatefulResource{}
	spec := StatefulSpec(s)
	meta, err := InitStateful(ctx, "openbase:core:StatefulResource", "custom-stateful", res, &res.StatefulMeta, spec, opts...)
	if err != nil {
		return nil, err
	}
	desc, err := buildEscapeHatchLink(ctx, meta, s.Links, s.Env, s.Secrets)
	if err != nil {
		return nil, err
	}
	res.SetLink(desc)
	if err := ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"branchFrom":    pulumi.String(meta.BranchFrom.String()),
	}); err != nil {
		return nil, err
	}
	return res, nil
}

// Private helpers ------------------------------------------------------------

func buildEscapeHatchLink(
	ctx *pulumi.Context,
	meta ResourceMeta,
	links []Resource,
	env map[string]pulumi.StringInput,
	secrets map[string]pulumi.StringInput,
) (LinkDescriptor, error) {
	resolved, err := ResolveLinks(LinkGrantContext{
		Ctx:          ctx,
		ConsumerName: meta.CanonicalName,
		Environment:  meta.Environment,
	}, links)
	if err != nil {
		return LinkDescriptor{}, err
	}
	for k, v := range env {
		resolved.Env[k] = v.ToStringOutput()
	}
	for k, v := range secrets {
		resolved.Secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return LinkDescriptor{
		Name:    meta.Name,
		Kind:    meta.Kind,
		Env:     resolved.Env,
		Secrets: resolved.Secrets,
	}, nil
}
