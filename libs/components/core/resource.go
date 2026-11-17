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

// Resource is satisfied by every OpenBase component.
type Resource interface {
	pulumi.Resource
	OpenBaseMeta() ResourceMeta
	Link() LinkDescriptor
}

// StatelessResource marks ephemeral/replaceable compute or routing components.
type StatelessResource interface {
	Resource
}

// StatefulResource marks components that hold persistent data and support BranchFrom.
type StatefulResource interface {
	Resource
	BranchSource() string
}

// StatelessResourceArgs configures shared metadata and Links for a StatelessResource.
type StatelessResourceArgs struct {
	Project     string
	Repo        string
	Environment string
	Kind        string
	Links       []Resource
	Env         map[string]pulumi.StringInput
	Secrets     map[string]pulumi.StringInput
}

// StatefulResourceArgs configures shared metadata, protection, and BranchFrom for a StatefulResource.
type StatefulResourceArgs struct {
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

// BuildStatelessMeta validates StatelessResourceArgs and produces deterministic metadata.
func BuildStatelessMeta(ctx *pulumi.Context, name string, args StatelessResourceArgs, defaultKind string) (ResourceMeta, error) {
	env, err := ResolveEnvironment(ctx, args.Environment)
	if err != nil {
		return ResourceMeta{}, err
	}
	canonical, err := CanonicalName(args.Project, env, name)
	if err != nil {
		return ResourceMeta{}, err
	}
	kind := args.Kind
	if kind == "" {
		kind = defaultKind
	}
	meta := ResourceMeta{
		Name:          name,
		CanonicalName: canonical,
		Project:       args.Project,
		Repo:          args.Repo,
		Kind:          kind,
		Environment:   env,
		Stateful:      false,
		Ephemeral:     env.IsEphemeral(),
		Protected:     false,
	}
	meta.Tags = StandardTags(meta)
	return meta, nil
}

// BuildStatefulMeta validates StatefulResourceArgs (including BranchFrom) and produces deterministic metadata.
func BuildStatefulMeta(ctx *pulumi.Context, name string, args StatefulResourceArgs, defaultKind string) (ResourceMeta, error) {
	env, err := ResolveEnvironment(ctx, args.Environment)
	if err != nil {
		return ResourceMeta{}, err
	}
	branchSrc, err := ValidateBranchSource(env, args.BranchFrom)
	if err != nil {
		return ResourceMeta{}, err
	}
	canonical, err := CanonicalName(args.Project, env, name)
	if err != nil {
		return ResourceMeta{}, err
	}
	kind := args.Kind
	if kind == "" {
		kind = defaultKind
	}
	protected := env.IsProtected()
	backup := protected
	if args.Backup != nil {
		backup = *args.Backup
	}
	meta := ResourceMeta{
		Name:          name,
		CanonicalName: canonical,
		Project:       args.Project,
		Repo:          args.Repo,
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

// InitStateless validates args and registers a provider StatelessResource component.
func InitStateless(
	ctx *pulumi.Context,
	typeToken, name, defaultKind string,
	self pulumi.ComponentResource,
	holder *StatelessMeta,
	args StatelessResourceArgs,
	opts ...pulumi.ResourceOption,
) (ResourceMeta, error) {
	meta, err := BuildStatelessMeta(ctx, name, args, defaultKind)
	if err != nil {
		return ResourceMeta{}, err
	}
	holder.meta = meta
	if err := ctx.RegisterComponentResource(typeToken, name, self, opts...); err != nil {
		return ResourceMeta{}, err
	}
	return meta, nil
}

// InitStateful validates args and registers a provider StatefulResource component.
func InitStateful(
	ctx *pulumi.Context,
	typeToken, name, defaultKind string,
	self pulumi.ComponentResource,
	holder *StatefulMeta,
	args StatefulResourceArgs,
	opts ...pulumi.ResourceOption,
) (ResourceMeta, error) {
	meta, err := BuildStatefulMeta(ctx, name, args, defaultKind)
	if err != nil {
		return ResourceMeta{}, err
	}
	holder.meta = meta
	if err := ctx.RegisterComponentResource(typeToken, name, self, opts...); err != nil {
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

// CustomStateless wraps an arbitrary Pulumi resource as a core.StatelessResource escape hatch.
type CustomStateless struct {
	pulumi.ResourceState
	StatelessMeta
}

// NewStatelessResource registers an escape-hatch StatelessResource with validated tags and Links.
func NewStatelessResource(ctx *pulumi.Context, name string, args StatelessResourceArgs, opts ...pulumi.ResourceOption) (*CustomStateless, error) {
	res := &CustomStateless{}
	meta, err := InitStateless(ctx, "openbase:core:StatelessResource", name, "custom-stateless", res, &res.StatelessMeta, args, opts...)
	if err != nil {
		return nil, err
	}
	desc, err := buildEscapeHatchLink(ctx, meta, args.Links, args.Env, args.Secrets)
	if err != nil {
		return nil, err
	}
	res.SetLink(desc)
	if err := ctx.RegisterResourceOutputs(res, pulumi.Map{"canonicalName": pulumi.String(meta.CanonicalName)}); err != nil {
		return nil, err
	}
	return res, nil
}

// CustomStateful wraps an arbitrary Pulumi resource as a core.StatefulResource escape hatch.
type CustomStateful struct {
	pulumi.ResourceState
	StatefulMeta
}

// NewStatefulResource registers an escape-hatch StatefulResource with validated BranchFrom, tags, and Links.
func NewStatefulResource(ctx *pulumi.Context, name string, args StatefulResourceArgs, opts ...pulumi.ResourceOption) (*CustomStateful, error) {
	res := &CustomStateful{}
	meta, err := InitStateful(ctx, "openbase:core:StatefulResource", name, "custom-stateful", res, &res.StatefulMeta, args, opts...)
	if err != nil {
		return nil, err
	}
	desc, err := buildEscapeHatchLink(ctx, meta, args.Links, args.Env, args.Secrets)
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
