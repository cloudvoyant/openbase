package core

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Public API -----------------------------------------------------------------

// ManifestOutputKey is the structured stack output key written by OpenBase components on a stack.
const ManifestOutputKey = "openbaseManifest"

// ErrAmbiguousResource reports multiple manifest entries of the same kind when no explicit name was passed to Lookup.
var ErrAmbiguousResource = errors.New("core: ambiguous manifest resource")

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
	localAcc  map[string]AccountResource
	localRes  map[string]Resource
}

// NewStackScopeRef wraps a Pulumi StackReference for cross-stack lookups.
func NewStackScopeRef(stackName string, sr *pulumi.StackReference) *ScopeRef {
	return &ScopeRef{stackName: stackName, sr: sr}
}

// NewLocalAccountScopeRef wraps in-memory AccountResource handles for single-process composition and tests.
func NewLocalAccountScopeRef(stackName string, items map[string]AccountResource) *ScopeRef {
	return &ScopeRef{stackName: stackName, localAcc: items}
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

// ManifestAttr resolves attrKey for a manifest resource matching explicit name[0] or unique kind,
// falling back to fallbackTopKey when the stack has no manifest entries for kind.
func (r *ScopeRef) ManifestAttr(kind string, name []string, attrKey string, fallbackTopKey ...string) pulumi.StringOutput {
	if r == nil {
		return pulumi.String("").ToStringOutput()
	}
	explicitName := firstNonEmpty(name)
	topKey := firstNonEmpty(fallbackTopKey)
	if topKey == "" {
		topKey = attrKey
	}
	if r.sr == nil {
		return r.resolveLocalManifestAttr(kind, explicitName, attrKey)
	}
	return r.resolveRemoteManifestAttr(kind, explicitName, attrKey, topKey)
}

// Resource returns a linkable RemoteResource handle for a named resource in the referenced stack.
func (r *ScopeRef) Resource(name string, opts ...RemoteOption) *RemoteResource {
	if r == nil {
		return nil
	}
	out := &RemoteResource{name: name, scope: r}
	if local, ok := r.localRes[name]; ok {
		out.local = local
	} else if acc, ok := r.localAcc[name]; ok {
		desc := acc.Link()
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

func firstNonEmpty(vals []string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (r *ScopeRef) resolveLocalManifestAttr(kind, explicitName, attrKey string) pulumi.StringOutput {
	desc, err := r.selectLocalDescriptor(kind, explicitName)
	if err != nil {
		return pulumi.String("").ToStringOutput().ApplyT(func(string) (string, error) {
			return "", err
		}).(pulumi.StringOutput)
	}
	if out, ok := desc.Attributes[attrKey]; ok {
		return out
	}
	return pulumi.String("").ToStringOutput()
}

func (r *ScopeRef) selectLocalDescriptor(kind, explicitName string) (LinkDescriptor, error) {
	if explicitName != "" {
		if acc, ok := r.localAcc[explicitName]; ok {
			return acc.Link(), nil
		}
		if res, ok := r.localRes[explicitName]; ok {
			return res.Link(), nil
		}
		return LinkDescriptor{}, nil
	}
	matches, names := r.collectLocalDescriptorsByKind(kind)
	if len(matches) > 1 {
		return LinkDescriptor{}, fmt.Errorf("%w: multiple %s entries (%s); pass explicit name", ErrAmbiguousResource, kind, strings.Join(names, ", "))
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return LinkDescriptor{}, nil
}

func (r *ScopeRef) collectLocalDescriptorsByKind(kind string) ([]LinkDescriptor, []string) {
	var matches []LinkDescriptor
	var names []string
	for k, acc := range r.localAcc {
		if d := acc.Link(); d.Kind == kind {
			matches = append(matches, d)
			names = append(names, k)
		}
	}
	for k, res := range r.localRes {
		if d := res.Link(); d.Kind == kind {
			matches = append(matches, d)
			names = append(names, k)
		}
	}
	slices.Sort(names)
	return matches, names
}

func (r *ScopeRef) resolveRemoteManifestAttr(kind, explicitName, attrKey, topKey string) pulumi.StringOutput {
	manifestOut := r.sr.GetOutput(pulumi.String(ManifestOutputKey))
	fallbackOut := pulumi.String("").ToStringOutput()
	if explicitName == "" && topKey != "" {
		fallbackOut = r.Output(topKey)
	}
	return pulumi.All(manifestOut, fallbackOut).ApplyT(func(vals []any) (string, error) {
		fallback, _ := vals[1].(string)
		return extractRemoteAttrValue(vals[0], kind, explicitName, attrKey, fallback)
	}).(pulumi.StringOutput)
}

func extractRemoteAttrValue(manifestRoot any, kind, explicitName, attrKey, fallback string) (string, error) {
	resources := extractManifestResourcesMap(manifestRoot)
	if explicitName != "" {
		entry, _ := resources[explicitName].(map[string]any)
		return readManifestEntryAttr(entry, attrKey), nil
	}
	matches, names := filterManifestEntriesByKind(resources, kind)
	if len(matches) > 1 {
		return "", fmt.Errorf("%w: multiple %s entries in manifest (%s); pass explicit name", ErrAmbiguousResource, kind, strings.Join(names, ", "))
	}
	if len(matches) == 1 {
		if v := readManifestEntryAttr(matches[0], attrKey); v != "" {
			return v, nil
		}
	}
	return fallback, nil
}

func extractManifestResourcesMap(root any) map[string]any {
	top, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	resources, _ := top["resources"].(map[string]any)
	return resources
}

func filterManifestEntriesByKind(resources map[string]any, kind string) ([]map[string]any, []string) {
	var matches []map[string]any
	var names []string
	for resName, raw := range resources {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if entryKind, _ := entry["kind"].(string); entryKind == kind {
			matches = append(matches, entry)
			names = append(names, resName)
		}
	}
	slices.Sort(names)
	return matches, names
}

func readManifestEntryAttr(entry map[string]any, attrKey string) string {
	if entry == nil {
		return ""
	}
	attrs, ok := entry["attributes"].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := attrs[attrKey].(string)
	return s
}

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
	resources := extractManifestResourcesMap(root)
	if resources == nil {
		return nil
	}
	entry, _ := resources[resName].(map[string]any)
	return entry
}
