package vercel

import (
	"reflect"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	vercelprov "github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatefulResource = (*BlobStoreResource)(nil)

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
	Env                      map[string]pulumi.StringInput
	Secrets                  map[string]pulumi.StringInput
	Shared                   *SharedResource
	SharedStackRef           string
	StoreID                  pulumi.StringInput
	VercelProjectID          pulumi.StringInput
	TeamID                   string
	Access                   string
	Region                   string
	StageCustomEnvironmentID pulumi.StringInput
	BlobReadWriteToken       pulumi.StringInput
}

// BlobStoreResource is the provisioned Vercel Blob store handle with tier-scoped BLOB_PREFIX and BLOB_FALLBACK_PREFIX.
type BlobStoreResource struct {
	pulumi.ResourceState
	core.StatefulMeta

	Store              *vercelprov.BlobStore
	Connection         *vercelprov.BlobProjectConnection
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
		Env:         b.Env,
		Secrets:     b.Secrets,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionBlobStore(ctx, b.Name, meta, b, res); err != nil {
		return nil, err
	}
	res.SetLink(buildBlobStoreLink(meta, b, res))
	if err := registerBlobOutputs(ctx, meta, res); err != nil {
		return nil, err
	}
	return res, nil
}

// Private helpers ------------------------------------------------------------

type resolvedBlobRefs struct {
	storeID    pulumi.StringOutput
	projectID  pulumi.StringOutput
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
	if spec.StageCustomEnvironmentID != nil {
		refs.stageEnvID = spec.StageCustomEnvironmentID.ToStringOutput()
	}
	applySharedBlobStruct(&refs, spec.Shared)
	if spec.SharedStackRef == "" {
		return refs, nil
	}
	return loadSharedBlobStackRef(ctx, name, spec.SharedStackRef, refs)
}

func applySharedBlobStruct(refs *resolvedBlobRefs, shared *SharedResource) {
	if shared == nil {
		return
	}
	if !isOutSet(refs.storeID) && shared.BlobStore != nil {
		refs.storeID = shared.BlobStoreID
	}
	if !isOutSet(refs.projectID) {
		refs.projectID = shared.ProjectID
	}
	if !isOutSet(refs.stageEnvID) {
		refs.stageEnvID = shared.StageCustomEnvironmentID
	}
}

func loadSharedBlobStackRef(
	ctx *pulumi.Context,
	name, refName string,
	refs resolvedBlobRefs,
) (resolvedBlobRefs, error) {
	sr, err := pulumi.NewStackReference(ctx, name+"-shared-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(refName)})
	if err != nil {
		return resolvedBlobRefs{}, err
	}
	if !isOutSet(refs.storeID) {
		refs.storeID = optionalVercelStackOutput(sr, "blobStoreId")
	}
	if !isOutSet(refs.projectID) {
		refs.projectID = preferVercelStackOutput(sr, "vercelProjectId", "projectId")
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

func propagateStageBlobEnv(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec BlobStore,
	refs resolvedBlobRefs,
	res *BlobStoreResource,
) error {
	if meta.Environment != core.EnvStage || !isOutSet(refs.projectID) || !isOutSet(refs.stageEnvID) {
		return nil
	}
	baseVar, err := vercelprov.NewProjectEnvironmentVariable(ctx, name+"-stage-base-url", &vercelprov.ProjectEnvironmentVariableArgs{
		ProjectId:            refs.projectID,
		TeamId:               optionalVercelStr(spec.TeamID),
		CustomEnvironmentIds: pulumi.StringArray{refs.stageEnvID},
		Key:                  pulumi.String("BLOB_BASE_URL"),
		Value:                res.BlobBaseURL,
		Sensitive:            pulumi.Bool(false),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.StageBaseURLEnvVar = baseVar
	if spec.BlobReadWriteToken == nil {
		return nil
	}
	tokVar, err := vercelprov.NewProjectEnvironmentVariable(ctx, name+"-stage-rw-token", &vercelprov.ProjectEnvironmentVariableArgs{
		ProjectId:            refs.projectID,
		TeamId:               optionalVercelStr(spec.TeamID),
		CustomEnvironmentIds: pulumi.StringArray{refs.stageEnvID},
		Key:                  pulumi.String("BLOB_READ_WRITE_TOKEN"),
		Value:                pulumi.ToSecret(spec.BlobReadWriteToken.ToStringOutput()).(pulumi.StringOutput),
		Sensitive:            pulumi.Bool(true),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.StageTokenEnvVar = tokVar
	return nil
}

func buildBlobStoreLink(meta core.ResourceMeta, spec BlobStore, res *BlobStoreResource) core.LinkDescriptor {
	envMap := map[string]pulumi.StringOutput{
		"BLOB_STORE_ID": res.StoreID,
		"BLOB_BASE_URL": res.BlobBaseURL,
		"BLOB_PREFIX":   pulumi.String(res.BlobPrefix).ToStringOutput(),
	}
	if res.BlobFallbackPrefix != "" {
		envMap["BLOB_FALLBACK_PREFIX"] = pulumi.String(res.BlobFallbackPrefix).ToStringOutput()
	}
	for k, v := range spec.Env {
		envMap[k] = v.ToStringOutput()
	}
	secrets := map[string]pulumi.StringOutput{}
	if spec.BlobReadWriteToken != nil {
		secrets["BLOB_READ_WRITE_TOKEN"] = pulumi.ToSecret(spec.BlobReadWriteToken.ToStringOutput()).(pulumi.StringOutput)
	}
	for k, v := range spec.Secrets {
		secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return core.LinkDescriptor{
		Name:    meta.Name,
		Kind:    meta.Kind,
		Env:     envMap,
		Secrets: secrets,
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
