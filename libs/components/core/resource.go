package core

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

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

// Resource is satisfied by every non-platform OpenBase resource handle (shared project assets and environment products).
type Resource interface {
	pulumi.Resource
	OpenBaseMeta() ResourceMeta
	Link() LinkDescriptor
}

// StatelessResource marks replaceable compute, routing, or project configuration handles.
type StatelessResource interface {
	Resource
}

// StatefulResource marks resource handles that hold persistent data and support BranchFrom.
type StatefulResource interface {
	Resource
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

// SetLink sets the LinkDescriptor exported by the stateless component without stack exports.
func (b *StatelessMeta) SetLink(desc LinkDescriptor) { b.link = desc }

// PublishLink sets the LinkDescriptor and records stack exports and openbaseManifest entries on ctx.
func (b *StatelessMeta) PublishLink(ctx *pulumi.Context, desc LinkDescriptor) {
	b.link = desc
	RecordStackExports(ctx, desc)
}

// StatefulMeta stores metadata, BranchSource, and Links on structs that directly embed pulumi.ResourceState.
type StatefulMeta struct {
	meta ResourceMeta
	link LinkDescriptor
}

// OpenBaseMeta returns the validated metadata for the stateful component.
func (b *StatefulMeta) OpenBaseMeta() ResourceMeta { return b.meta }

// Link returns the LinkDescriptor exported by the stateful component.
func (b *StatefulMeta) Link() LinkDescriptor { return b.link }

// SetLink sets the LinkDescriptor exported by the stateful component without stack exports.
func (b *StatefulMeta) SetLink(desc LinkDescriptor) { b.link = desc }

// PublishLink sets the LinkDescriptor and records stack exports and openbaseManifest entries on ctx.
func (b *StatefulMeta) PublishLink(ctx *pulumi.Context, desc LinkDescriptor) {
	b.link = desc
	RecordStackExports(ctx, desc)
}

// BranchSource returns the persistent source environment when branching, or "".
func (b *StatefulMeta) BranchSource() string { return b.meta.BranchFrom.String() }

// ResolveProject returns explicit when non-empty, otherwise ctx.Project() with any "-shared" suffix trimmed.
func ResolveProject(ctx *pulumi.Context, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if ctx == nil {
		return ""
	}
	return strings.TrimSuffix(ctx.Project(), "-shared")
}

// RequireSharedStack validates that the active stack resolves to core.EnvShared for <provider>.NewProject constructors.
func RequireSharedStack(ctx *pulumi.Context) error {
	env, err := ResolveEnvironment(ctx, "")
	if err != nil {
		return err
	}
	if env != EnvShared {
		return fmt.Errorf("%w: Project resources require the shared stack, got %q", ErrInvalidEnvironment, env)
	}
	return nil
}

// BuildStatelessMeta validates StatelessSpec and produces deterministic metadata.
func BuildStatelessMeta(ctx *pulumi.Context, spec StatelessSpec, defaultKind string) (ResourceMeta, error) {
	env, err := ResolveEnvironment(ctx, spec.Environment)
	if err != nil {
		return ResourceMeta{}, err
	}
	project := ResolveProject(ctx, spec.Project)
	canonical, err := CanonicalName(project, env, spec.Name)
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
		Project:       project,
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
	project := ResolveProject(ctx, spec.Project)
	canonical, err := CanonicalName(project, env, spec.Name)
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
		Project:       project,
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

// StatelessArgs configures an escape-hatch StatelessResource component wrapping custom Pulumi resources.
type StatelessArgs struct {
	ProjectName string
	Repo        string
	Environment string
	Kind        string
	Links       []Resource
	Attributes  map[string]pulumi.StringInput
	GrantAccess func(LinkGrantContext) error
}

// Stateless is the provisioned handle returned by NewStateless.
type Stateless struct {
	pulumi.ResourceState
	StatelessMeta
}

// NewStateless registers an escape-hatch StatelessResource with canonical tags and access Links.
func NewStateless(ctx *pulumi.Context, name string, args *StatelessArgs, opts ...pulumi.ResourceOption) (*Stateless, error) {
	if args == nil {
		args = &StatelessArgs{}
	}
	res := &Stateless{}
	meta, err := InitStateless(ctx, "openbase:core:StatelessResource", "custom-stateless", res, &res.StatelessMeta, StatelessSpec{
		Name:        name,
		Project:     args.ProjectName,
		Repo:        args.Repo,
		Environment: args.Environment,
		Kind:        args.Kind,
		Links:       args.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	desc, err := buildEscapeHatchLink(ctx, meta, args.Links, args.Attributes, args.GrantAccess)
	if err != nil {
		return nil, err
	}
	res.PublishLink(ctx, desc)
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{"canonicalName": pulumi.String(meta.CanonicalName)})
}

// StatefulArgs configures an escape-hatch StatefulResource component wrapping custom Pulumi resources.
type StatefulArgs struct {
	ProjectName string
	Repo        string
	Environment string
	Kind        string
	BranchFrom  string
	Backup      *bool
	Links       []Resource
	Attributes  map[string]pulumi.StringInput
	GrantAccess func(LinkGrantContext) error
}

// Stateful is the provisioned handle returned by NewStateful.
type Stateful struct {
	pulumi.ResourceState
	StatefulMeta
}

// NewStateful registers an escape-hatch StatefulResource with BranchFrom, tags, and access Links.
func NewStateful(ctx *pulumi.Context, name string, args *StatefulArgs, opts ...pulumi.ResourceOption) (*Stateful, error) {
	if args == nil {
		args = &StatefulArgs{}
	}
	res := &Stateful{}
	meta, err := InitStateful(ctx, "openbase:core:StatefulResource", "custom-stateful", res, &res.StatefulMeta, StatefulSpec{
		Name:        name,
		Project:     args.ProjectName,
		Repo:        args.Repo,
		Environment: args.Environment,
		Kind:        args.Kind,
		BranchFrom:  args.BranchFrom,
		Backup:      args.Backup,
		Links:       args.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	desc, err := buildEscapeHatchLink(ctx, meta, args.Links, args.Attributes, args.GrantAccess)
	if err != nil {
		return nil, err
	}
	res.PublishLink(ctx, desc)
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"branchFrom":    pulumi.String(meta.BranchFrom.String()),
	})
}

// Private helpers ------------------------------------------------------------

func buildEscapeHatchLink(
	ctx *pulumi.Context,
	meta ResourceMeta,
	links []Resource,
	attrs map[string]pulumi.StringInput,
	grantAccess func(LinkGrantContext) error,
) (LinkDescriptor, error) {
	_, err := ResolveLinks(LinkGrantContext{
		Ctx:          ctx,
		ConsumerName: meta.CanonicalName,
		ConsumerMeta: meta,
		Environment:  meta.Environment,
	}, links)
	if err != nil {
		return LinkDescriptor{}, err
	}
	attrMap := map[string]pulumi.StringOutput{}
	for k, v := range attrs {
		attrMap[k] = v.ToStringOutput()
	}
	return LinkDescriptor{
		Name:        meta.Name,
		Kind:        meta.Kind,
		Attributes:  attrMap,
		GrantAccess: grantAccess,
	}, nil
}
