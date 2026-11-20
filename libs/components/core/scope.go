package core

import (
	"fmt"
	"sync"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Public API -----------------------------------------------------------------

// ManifestOutputKey is the structured stack output key written by Platform, Project, and Environment stacks.
const ManifestOutputKey = "openbaseManifest"

// Scope carries inherited Platform, Project, Environment, and IdentityBroker context into scoped resource registrations.
type Scope struct {
	Org        string
	Project    string
	Repo       string
	Env        Environment
	Platform   *PlatformRef
	ProjectRef *ProjectRef
	Brokers    []IdentityBroker
}

// DefaultDBBranchFrom returns "prod" on stage, "dev" on preview-*, and "" otherwise.
func (s Scope) DefaultDBBranchFrom() string {
	if s.Env == EnvStage {
		return string(EnvProd)
	}
	if s.Env.IsPreview() {
		return string(EnvDev)
	}
	return ""
}

// DefaultStorageBranchFrom returns "dev" on preview-* and "" on persistent stacks.
func (s Scope) DefaultStorageBranchFrom() string {
	if s.Env.IsPreview() {
		return string(EnvDev)
	}
	return ""
}

// BranchStackRef formats "<org>/<project>/<branch>" when branch is non-empty.
func (s Scope) BranchStackRef(ctx *pulumi.Context, branch string) string {
	if branch == "" {
		return ""
	}
	org := ctx.Organization()
	if org == "" {
		org = s.Org
	}
	proj := ctx.Project()
	if proj == "" {
		proj = s.Project
	}
	return fmt.Sprintf("%s/%s/%s", org, proj, branch)
}

// PlatformStackName returns the active Platform stack reference string, or "".
func (s Scope) PlatformStackName() string {
	if s.Platform == nil {
		return ""
	}
	return s.Platform.StackName()
}

// ProjectStackName returns the active Project stack reference string, or "".
func (s Scope) ProjectStackName() string {
	if s.ProjectRef == nil {
		return ""
	}
	return s.ProjectRef.StackName()
}

// PlatformSpecItem is implemented by organization-level specs registered inside openbase.Platform.
type PlatformSpecItem interface {
	RegisterPlatformInScope(ctx *pulumi.Context, scope Scope, opts ...pulumi.ResourceOption) (PlatformResource, error)
}

// PlatformSpecItemOf preserves the concrete PlatformResource pointer type R when calling openbase.AddPlatform.
type PlatformSpecItemOf[R PlatformResource] interface {
	PlatformSpecItem
	RegisterTypedPlatformInScope(ctx *pulumi.Context, scope Scope, opts ...pulumi.ResourceOption) (R, error)
}

// ScopedSpec is implemented by shared or environment specs registered inside openbase.Project or openbase.Environment.
type ScopedSpec interface {
	RegisterInScope(ctx *pulumi.Context, scope Scope, opts ...pulumi.ResourceOption) (Resource, error)
}

// ScopedSpecOf preserves the concrete resource pointer type R when calling openbase.Add.
type ScopedSpecOf[R Resource] interface {
	ScopedSpec
	RegisterTypedInScope(ctx *pulumi.Context, scope Scope, opts ...pulumi.ResourceOption) (R, error)
}

// RemoteGrantHandler binds IAM/access on a remote Project or Platform resource referenced via ScopeRef.Resource(name).
type RemoteGrantHandler func(grantCtx LinkGrantContext, remote *RemoteResource) error

// RegisterRemoteGrantHandler registers an access-grant handler for a remote resource kind (e.g. "gcp-bucket").
func RegisterRemoteGrantHandler(kind string, fn RemoteGrantHandler) {
	remoteGrantMu.Lock()
	defer remoteGrantMu.Unlock()
	remoteGrantHandlers[kind] = fn
}

// RemoteOption configures remote manifest lookup options for ScopeRef.Resource over a StackReference.
type RemoteOption func(*RemoteResource)

// WithRemoteKind specifies the remote resource kind (e.g. "gcp-bucket") so GrantAccess runs synchronously.
func WithRemoteKind(kind string) RemoteOption {
	return func(r *RemoteResource) { r.kind = kind }
}

// ScopeRef provides typed lookup over an in-memory or StackReference-backed Platform or Project scope.
type ScopeRef struct {
	stackName string
	sr        *pulumi.StackReference
	localPlat map[string]PlatformResource
	localRes  map[string]Resource
}

// NewStackScopeRef wraps a Pulumi StackReference for cross-stack lookups.
func NewStackScopeRef(stackName string, sr *pulumi.StackReference) *ScopeRef {
	return &ScopeRef{stackName: stackName, sr: sr}
}

// NewLocalPlatformScopeRef wraps in-memory PlatformResource handles for single-process composition and tests.
func NewLocalPlatformScopeRef(stackName string, items map[string]PlatformResource) *ScopeRef {
	return &ScopeRef{stackName: stackName, localPlat: items}
}

// NewLocalResourceScopeRef wraps in-memory Resource handles for single-process composition and tests.
func NewLocalResourceScopeRef(stackName string, items map[string]Resource) *ScopeRef {
	return &ScopeRef{stackName: stackName, localRes: items}
}

// StackName returns the StackReference identifier ("<org>/<project>/<stack>"), or "" when purely in-memory.
func (r *ScopeRef) StackName() string {
	if r == nil {
		return ""
	}
	return r.stackName
}

// StackReference returns the underlying Pulumi StackReference when backed by remote state.
func (r *ScopeRef) StackReference() *pulumi.StackReference {
	if r == nil {
		return nil
	}
	return r.sr
}

// Output reads a top-level stack output key as a StringOutput (returning "" when absent).
func (r *ScopeRef) Output(key string) pulumi.StringOutput {
	if r == nil || r.sr == nil {
		return pulumi.String("").ToStringOutput()
	}
	return r.sr.GetOutput(pulumi.String(key)).ApplyT(func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}).(pulumi.StringOutput)
}

// Resource returns a linkable RemoteResource handle for a named resource in the parent Platform or Project stack.
func (r *ScopeRef) Resource(name string, opts ...RemoteOption) *RemoteResource {
	if r == nil {
		return nil
	}
	out := &RemoteResource{name: name, scope: r}
	if local, ok := r.localRes[name]; ok {
		out.local = local
	} else if plat, ok := r.localPlat[name]; ok {
		desc := plat.Link()
		out.localDesc = &desc
	}
	for _, opt := range opts {
		opt(out)
	}
	return out
}

// PlatformHandleByName returns an in-memory PlatformResource by logical name when available in the same process.
func (r *ScopeRef) PlatformHandleByName(name string) PlatformResource {
	if r == nil {
		return nil
	}
	return r.localPlat[name]
}

// LookupManifestAttr reads a string attribute for a named resource inside a remote stack's openbaseManifest output.
func LookupManifestAttr(sr *pulumi.StackReference, resName, key string) pulumi.StringOutput {
	if sr == nil || resName == "" || key == "" {
		return pulumi.String("").ToStringOutput()
	}
	return lookupManifestMapField(sr, resName, "attributes", key)
}

// PlatformHandle returns an in-memory PlatformResource by kind when available in the same process.
func (r *ScopeRef) PlatformHandle(kind string) PlatformResource {
	if r == nil {
		return nil
	}
	for _, item := range r.localPlat {
		if item.OpenBasePlatform().Kind == kind {
			return item
		}
	}
	return nil
}

// ResourceHandle returns an in-memory Resource by kind when available in the same process.
func (r *ScopeRef) ResourceHandle(kind string) Resource {
	if r == nil {
		return nil
	}
	for _, item := range r.localRes {
		if item.OpenBaseMeta().Kind == kind {
			return item
		}
	}
	return nil
}

// PlatformRef references an organization Platform stack.
type PlatformRef struct {
	*ScopeRef
	Org string
}

// ProjectRef references a repository's shared Project stack (and optional PlatformRef).
type ProjectRef struct {
	*ScopeRef
	Project  string
	Platform *PlatformRef
}

// RemoteResource adapts a resource exported in a parent Platform or Project stack into a local core.Resource for Links.
type RemoteResource struct {
	pulumi.ResourceState
	name      string
	kind      string
	scope     *ScopeRef
	local     Resource
	localDesc *LinkDescriptor
}

// Name returns the resource's logical name inside the parent stack.
func (r *RemoteResource) Name() string { return r.name }

// OpenBaseMeta returns synthetic shared metadata for the remote resource.
func (r *RemoteResource) OpenBaseMeta() ResourceMeta {
	if r.local != nil {
		return r.local.OpenBaseMeta()
	}
	return ResourceMeta{Name: r.name, Kind: r.kind, Environment: EnvShared}
}

// Link reconstructs a LinkDescriptor from in-memory state or the parent stack's openbaseManifest output.
func (r *RemoteResource) Link() LinkDescriptor {
	if r.local != nil {
		return r.local.Link()
	}
	if r.localDesc != nil {
		return *r.localDesc
	}
	return r.remoteManifestLink()
}

// Attr reads an attribute exported in the remote resource's manifest entry.
func (r *RemoteResource) Attr(key string) pulumi.StringOutput {
	if r == nil {
		return pulumi.String("").ToStringOutput()
	}
	if r.local != nil {
		if out, ok := r.local.Link().Attributes[key]; ok {
			return out
		}
	}
	if r.localDesc != nil {
		if out, ok := r.localDesc.Attributes[key]; ok {
			return out
		}
	}
	if r.scope == nil || r.scope.sr == nil {
		return pulumi.String("").ToStringOutput()
	}
	return lookupManifestMapField(r.scope.sr, r.name, "attributes", key)
}

// BuildManifestMap serializes LinkDescriptors into the openbaseManifest Pulumi output map.
func BuildManifestMap(org, project, platformStack string, descriptors []LinkDescriptor) pulumi.Map {
	resources := pulumi.Map{}
	for _, d := range descriptors {
		if d.Name == "" {
			continue
		}
		resources[d.Name] = pulumi.Map{
			"kind":       pulumi.String(d.Kind),
			"attributes": toOutputMap(d.Attributes),
		}
	}
	return pulumi.Map{
		"org":           pulumi.String(org),
		"project":       pulumi.String(project),
		"platformStack": pulumi.String(platformStack),
		"resources":     resources,
	}
}

// Private helpers ------------------------------------------------------------

var (
	remoteGrantMu       sync.RWMutex
	remoteGrantHandlers = map[string]RemoteGrantHandler{}
)

func toOutputMap(in map[string]pulumi.StringOutput) pulumi.Map {
	m := pulumi.Map{}
	for k, v := range in {
		m[k] = v
	}
	return m
}

func (r *RemoteResource) remoteManifestLink() LinkDescriptor {
	return LinkDescriptor{
		Name: r.name,
		Kind: r.kind,
		GrantAccess: func(grantCtx LinkGrantContext) error {
			if r.kind == "" {
				return nil
			}
			remoteGrantMu.RLock()
			fn := remoteGrantHandlers[r.kind]
			remoteGrantMu.RUnlock()
			if fn == nil {
				return nil
			}
			return fn(grantCtx, r)
		},
	}
}

func lookupManifestMapField(sr *pulumi.StackReference, resName, section, key string) pulumi.StringOutput {
	return sr.GetOutput(pulumi.String(ManifestOutputKey)).ApplyT(func(v any) string {
		resMap := extractManifestResource(v, resName)
		sec, ok := resMap[section].(map[string]any)
		if !ok {
			return ""
		}
		if s, ok := sec[key].(string); ok {
			return s
		}
		return ""
	}).(pulumi.StringOutput)
}

func extractManifestResource(root any, resName string) map[string]any {
	top, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	resources, ok := top["resources"].(map[string]any)
	if !ok {
		return nil
	}
	entry, _ := resources[resName].(map[string]any)
	return entry
}
