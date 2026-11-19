package vercel

import (
	"reflect"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	vercelprov "github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatefulResource                 = (*BlobStoreResource)(nil)
	_ core.ScopedSpecOf[*BlobStoreResource] = BlobStore{}
)

// BlobStore declares a Vercel BlobStore component with custom-env token propagation and prefix overlays.
type BlobStore struct {
	Name                     string
	Project                  string
	Repo                     string
	Environment              string
	Kind                     string
	BranchFrom               string
	Backup                   *bool
	Links                    []core.Resource
	VercelProject            *ProjectResource
	ProjectStackRef          string
	StoreID                  pulumi.StringInput
	VercelProjectID          pulumi.StringInput
	TeamID                   string
	Access                   string
	Region                   string
	EnableDevEnvironment     bool
	SkipStageEnvironment     bool
	DevCustomEnvironmentID   pulumi.StringInput
	StageCustomEnvironmentID pulumi.StringInput
	BlobReadWriteToken       pulumi.StringInput
}

// BlobStoreResource is the provisioned Vercel Blob store handle with tier-scoped BLOB_PREFIX and BLOB_FALLBACK_PREFIX.
type BlobStoreResource struct {
	pulumi.ResourceState
	core.StatefulMeta

	Store              *vercelprov.BlobStore
	Connection         *vercelprov.BlobProjectConnection
	DevTokenEnvVar     *vercelprov.ProjectEnvironmentVariable
	DevBaseURLEnvVar   *vercelprov.ProjectEnvironmentVariable
	StageTokenEnvVar   *vercelprov.ProjectEnvironmentVariable
	StageBaseURLEnvVar *vercelprov.ProjectEnvironmentVariable
	StoreID            pulumi.StringOutput
	BlobBaseURL        pulumi.StringOutput
	BlobPrefix         string
	BlobFallbackPrefix string
}

// Register provisions or attaches a Vercel BlobStore satisfying core.StatefulResource.
func (b BlobStore) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*BlobStoreResource, error) {
	res := &BlobStoreResource{}
	meta, err := core.InitStateful(ctx, "openbase:vercel:BlobStore", "vercel-blob", res, &res.StatefulMeta, core.StatefulSpec{
		Name:        b.Name,
		Project:     b.Project,
		Repo:        b.Repo,
		Environment: b.Environment,
		Kind:        b.Kind,
		BranchFrom:  b.BranchFrom,
		Backup:      b.Backup,
		Links:       b.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionBlobStore(ctx, b.Name, meta, b, res); err != nil {
		return nil, err
	}
	res.SetLink(buildBlobStoreLink(meta, res))
	if err := registerBlobOutputs(ctx, meta, res); err != nil {
		return nil, err
	}
	return res, nil
}

// RegisterInScope adapts vercel.BlobStore to openbase.Project and openbase.Environment.
func (b BlobStore) RegisterInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (core.Resource, error) {
	return b.RegisterTypedInScope(ctx, scope, opts...)
}

// RegisterTypedInScope registers vercel.BlobStore with inherited Scope metadata, BranchFrom, and ProjectStackRef.
func (b BlobStore) RegisterTypedInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (*BlobStoreResource, error) {
	b.Project = defaultVercelStr(b.Project, scope.Project)
	b.Repo = defaultVercelStr(b.Repo, scope.Repo)
	b.Environment = defaultVercelStr(b.Environment, string(scope.Env))
	b.BranchFrom = defaultVercelStr(b.BranchFrom, scope.DefaultStorageBranchFrom())
	if b.VercelProject == nil && scope.ProjectRef != nil {
		if vp, ok := scope.ProjectRef.ResourceHandle("vercel-project").(*ProjectResource); ok {
			b.VercelProject = vp
		}
	}
	if b.ProjectStackRef == "" && b.VercelProject == nil && b.StoreID == nil {
		b.ProjectStackRef = scope.ProjectStackName()
	}
	return b.Register(ctx, opts...)
}

// Private helpers ------------------------------------------------------------

type resolvedBlobRefs struct {
	storeID    pulumi.StringOutput
	projectID  pulumi.StringOutput
	devEnvID   pulumi.StringOutput
	stageEnvID pulumi.StringOutput
}

func provisionBlobStore(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec BlobStore,
	res *BlobStoreResource,
) error {
	refs, err := resolveBlobRefs(ctx, name, spec)
	if err != nil {
		return err
	}
	res.BlobPrefix = meta.Environment.String() + "/"
	if meta.BranchFrom != "" {
		res.BlobFallbackPrefix = meta.BranchFrom.String() + "/"
	}
	if err := ensureUnderlyingBlobStore(ctx, name, meta, spec, &refs, res); err != nil {
		return err
	}
	res.StoreID = refs.storeID
	res.BlobBaseURL = DeriveBlobBaseURL(refs.storeID)
	if err := propagateDevBlobEnv(ctx, name, meta, spec, refs, res); err != nil {
		return err
	}
	return propagateStageBlobEnv(ctx, name, meta, spec, refs, res)
}

func resolveBlobRefs(ctx *pulumi.Context, name string, spec BlobStore) (resolvedBlobRefs, error) {
	var refs resolvedBlobRefs
	if spec.StoreID != nil {
		refs.storeID = spec.StoreID.ToStringOutput()
	}
	if spec.VercelProjectID != nil {
		refs.projectID = spec.VercelProjectID.ToStringOutput()
	}
	if spec.DevCustomEnvironmentID != nil {
		refs.devEnvID = spec.DevCustomEnvironmentID.ToStringOutput()
	}
	if spec.StageCustomEnvironmentID != nil {
		refs.stageEnvID = spec.StageCustomEnvironmentID.ToStringOutput()
	}
	applyVercelProjectBlobStruct(&refs, spec.VercelProject)
	if spec.ProjectStackRef == "" {
		return refs, nil
	}
	return loadProjectBlobStackRef(ctx, name, spec.ProjectStackRef, refs)
}

func applyVercelProjectBlobStruct(refs *resolvedBlobRefs, proj *ProjectResource) {
	if proj == nil {
		return
	}
	if !isOutSet(refs.storeID) && proj.BlobStore != nil {
		refs.storeID = proj.BlobStoreID
	}
	if !isOutSet(refs.projectID) {
		refs.projectID = proj.ProjectID
	}
	if !isOutSet(refs.devEnvID) && proj.DevEnvironment != nil {
		refs.devEnvID = proj.DevCustomEnvironmentID
	}
	if !isOutSet(refs.stageEnvID) && proj.StageEnvironment != nil {
		refs.stageEnvID = proj.StageCustomEnvironmentID
	}
}

func loadProjectBlobStackRef(
	ctx *pulumi.Context,
	name, refName string,
	refs resolvedBlobRefs,
) (resolvedBlobRefs, error) {
	sr, err := pulumi.NewStackReference(ctx, name+"-project-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(refName)})
	if err != nil {
		return resolvedBlobRefs{}, err
	}
	if !isOutSet(refs.storeID) {
		refs.storeID = optionalVercelStackOutput(sr, "blobStoreId")
	}
	if !isOutSet(refs.projectID) {
		refs.projectID = preferVercelStackOutput(sr, "vercelProjectId", "projectId")
	}
	if !isOutSet(refs.devEnvID) {
		refs.devEnvID = optionalVercelStackOutput(sr, "devCustomEnvironmentId")
	}
	if !isOutSet(refs.stageEnvID) {
		refs.stageEnvID = optionalVercelStackOutput(sr, "stageCustomEnvironmentId")
	}
	return refs, nil
}

func ensureUnderlyingBlobStore(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec BlobStore,
	refs *resolvedBlobRefs,
	res *BlobStoreResource,
) error {
	if isOutSet(refs.storeID) {
		return nil
	}
	store, err := vercelprov.NewBlobStore(ctx, name+"-store", &vercelprov.BlobStoreArgs{
		Name:   pulumi.String(core.BoundedName(meta.CanonicalName, 52)),
		Access: pulumi.String(defaultVercelStr(spec.Access, "public")),
		Region: pulumi.String(defaultVercelStr(spec.Region, "iad1")),
		TeamId: optionalVercelStr(spec.TeamID),
	}, core.ChildOptions(res, meta)...)
	if err != nil {
		return err
	}
	res.Store = store
	refs.storeID = store.ID().ToStringOutput()
	if !isOutSet(refs.projectID) {
		return nil
	}
	conn, err := vercelprov.NewBlobProjectConnection(ctx, name+"-conn", &vercelprov.BlobProjectConnectionArgs{
		BlobStoreId:  store.ID(),
		ProjectId:    refs.projectID,
		TeamId:       optionalVercelStr(spec.TeamID),
		Environments: pulumi.ToStringArray([]string{"development", "preview", "production"}),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Connection = conn
	return nil
}

func propagateDevBlobEnv(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec BlobStore,
	refs resolvedBlobRefs,
	res *BlobStoreResource,
) error {
	hasDevCustom := spec.EnableDevEnvironment || (spec.VercelProject != nil && spec.VercelProject.DevEnvironment != nil)
	if meta.Environment != core.EnvDev || !hasDevCustom || !isOutSet(refs.projectID) || !isOutSet(refs.devEnvID) {
		return nil
	}
	baseVar, tokVar, err := createCustomEnvBlobVars(ctx, name+"-dev", refs.projectID, refs.devEnvID, spec, res)
	if err != nil {
		return err
	}
	res.DevBaseURLEnvVar = baseVar
	res.DevTokenEnvVar = tokVar
	return nil
}

func propagateStageBlobEnv(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec BlobStore,
	refs resolvedBlobRefs,
	res *BlobStoreResource,
) error {
	if meta.Environment != core.EnvStage || spec.SkipStageEnvironment || !isOutSet(refs.projectID) || !isOutSet(refs.stageEnvID) {
		return nil
	}
	baseVar, tokVar, err := createCustomEnvBlobVars(ctx, name+"-stage", refs.projectID, refs.stageEnvID, spec, res)
	if err != nil {
		return err
	}
	res.StageBaseURLEnvVar = baseVar
	res.StageTokenEnvVar = tokVar
	return nil
}

func createCustomEnvBlobVars(
	ctx *pulumi.Context,
	prefix string,
	projectID, customEnvID pulumi.StringOutput,
	spec BlobStore,
	res *BlobStoreResource,
) (*vercelprov.ProjectEnvironmentVariable, *vercelprov.ProjectEnvironmentVariable, error) {
	baseVar, err := vercelprov.NewProjectEnvironmentVariable(ctx, prefix+"-base-url", &vercelprov.ProjectEnvironmentVariableArgs{
		ProjectId:            projectID,
		TeamId:               optionalVercelStr(spec.TeamID),
		CustomEnvironmentIds: pulumi.StringArray{customEnvID},
		Key:                  pulumi.String("BLOB_BASE_URL"),
		Value:                res.BlobBaseURL,
		Sensitive:            pulumi.Bool(false),
	}, pulumi.Parent(res))
	if err != nil || spec.BlobReadWriteToken == nil {
		return baseVar, nil, err
	}
	tokVar, err := vercelprov.NewProjectEnvironmentVariable(ctx, prefix+"-rw-token", &vercelprov.ProjectEnvironmentVariableArgs{
		ProjectId:            projectID,
		TeamId:               optionalVercelStr(spec.TeamID),
		CustomEnvironmentIds: pulumi.StringArray{customEnvID},
		Key:                  pulumi.String("BLOB_READ_WRITE_TOKEN"),
		Value:                pulumi.ToSecret(spec.BlobReadWriteToken.ToStringOutput()).(pulumi.StringOutput),
		Sensitive:            pulumi.Bool(true),
	}, pulumi.Parent(res))
	return baseVar, tokVar, err
}

func buildBlobStoreLink(meta core.ResourceMeta, res *BlobStoreResource) core.LinkDescriptor {
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"storeId":            res.StoreID,
			"blobBaseUrl":        res.BlobBaseURL,
			"blobPrefix":         pulumi.String(res.BlobPrefix).ToStringOutput(),
			"blobFallbackPrefix": pulumi.String(res.BlobFallbackPrefix).ToStringOutput(),
		},
		StackExports: map[string]pulumi.Input{
			"blobBaseUrl":        res.BlobBaseURL,
			"blobPrefix":         pulumi.String(res.BlobPrefix),
			"blobFallbackPrefix": pulumi.String(res.BlobFallbackPrefix),
		},
	}
}

func registerBlobOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *BlobStoreResource) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName":      pulumi.String(meta.CanonicalName),
		"storeId":            res.StoreID,
		"blobBaseUrl":        res.BlobBaseURL,
		"blobPrefix":         pulumi.String(res.BlobPrefix),
		"blobFallbackPrefix": pulumi.String(res.BlobFallbackPrefix),
		"branchFrom":         pulumi.String(meta.BranchFrom.String()),
	})
}

func isOutSet(out pulumi.StringOutput) bool {
	return !reflect.ValueOf(out).IsZero()
}
