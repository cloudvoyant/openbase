package core

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Public API -----------------------------------------------------------------

// ManifestOutputKey is the structured stack output key written by OpenBase components on a stack.
const ManifestOutputKey = "openbaseManifest"

// DefaultBranchFrom returns the conventional parent branch ("prod" on stage when stageFromProd is true, "dev" on preview-*).
func DefaultBranchFrom(env Environment, explicit string, stageFromProd bool) string {
	if explicit != "" {
		return explicit
	}
	if stageFromProd && env == EnvStage {
		return string(EnvProd)
	}
	if env.IsPreview() {
		return string(EnvDev)
	}
	return ""
}

// DefaultBranchStackRef formats "<org>/<project>/<branchFrom>" when branchFrom is non-empty and explicitRef is empty.
func DefaultBranchStackRef(ctx *pulumi.Context, project, branchFrom, explicitRef string) string {
	if explicitRef != "" || branchFrom == "" || ctx == nil {
		return explicitRef
	}
	org := ctx.Organization()
	if org == "" {
		org = "organization"
	}
	proj := ctx.Project()
	if proj == "" {
		proj = project
	}
	return fmt.Sprintf("%s/%s/%s", org, proj, branchFrom)
}

// RecordStackExports exports non-colliding StackExports and updates openbaseManifest on ctx.
func RecordStackExports(ctx *pulumi.Context, desc LinkDescriptor) {
	if ctx == nil {
		return
	}
	exportTrackersMu.Lock()
	tr, ok := exportTrackers[ctx]
	if !ok {
		tr = &ctxExportTracker{exported: map[string]bool{}}
		exportTrackers[ctx] = tr
	}
	for k, v := range desc.StackExports {
		if tr.exported[k] {
			continue
		}
		tr.exported[k] = true
		ctx.Export(k, v)
	}
	if desc.Name != "" {
		tr.descriptors = append(tr.descriptors, desc)
	}
	descriptors := append([]LinkDescriptor(nil), tr.descriptors...)
	exportTrackersMu.Unlock()

	org := OrgScope(ctx.Organization())
	project := strings.TrimSuffix(ctx.Project(), "-shared")
	if project == "platform" {
		project = ""
	}
	ctx.Export(ManifestOutputKey, BuildManifestMap(org, project, "", descriptors))
}

// RemoteGrantHandler binds IAM/access on a remote shared or platform resource referenced via ScopeRef.Resource(name).
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

// ScopeRef provides typed output and resource lookup over an in-memory map or a Pulumi StackReference.
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

// ResolveStackReference returns r.StackReference() when non-nil, or creates a new StackReference for explicitRef.
func ResolveStackReference(ctx *pulumi.Context, resName string, ref *ScopeRef, explicitRef string) (*pulumi.StackReference, error) {
	if ref != nil && ref.sr != nil {
		return ref.sr, nil
	}
	stackName := explicitRef
	if stackName == "" && ref != nil {
		stackName = ref.stackName
	}
	if stackName == "" {
		return nil, nil
	}
	return pulumi.NewStackReference(ctx, resName, &pulumi.StackReferenceArgs{
		Name: pulumi.String(stackName),
	})
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

// Resource returns a linkable RemoteResource handle for a named resource in the referenced stack.
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

// LookupManifestAttr reads a string attribute for a named resource inside a remote stack's openbaseManifest output.
func LookupManifestAttr(sr *pulumi.StackReference, resName, key string) pulumi.StringOutput {
	if sr == nil || resName == "" || key == "" {
		return pulumi.String("").ToStringOutput()
	}
	return lookupManifestMapField(sr, resName, "attributes", key)
}

// RemoteResource adapts a resource exported in a remote stack into a local core.Resource for Links.
type RemoteResource struct {
	pulumi.ResourceState
	name      string
	kind      string
	scope     *ScopeRef
	local     Resource
	localDesc *LinkDescriptor
}

// Name returns the resource's logical name inside the referenced stack.
func (r *RemoteResource) Name() string { return r.name }

// OpenBaseMeta returns synthetic shared metadata for the remote resource.
func (r *RemoteResource) OpenBaseMeta() ResourceMeta {
	if r.local != nil {
		return r.local.OpenBaseMeta()
	}
	return ResourceMeta{Name: r.name, Kind: r.kind, Environment: EnvShared}
}

// Link reconstructs a LinkDescriptor from in-memory state or the referenced stack's openbaseManifest output.
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

type ctxExportTracker struct {
	exported    map[string]bool
	descriptors []LinkDescriptor
}

var (
	exportTrackersMu sync.Mutex
	exportTrackers   = map[*pulumi.Context]*ctxExportTracker{}

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
