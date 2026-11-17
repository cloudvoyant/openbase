package vercel

import (
	"reflect"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	vercelprov "github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatefulResource = (*BlobStore)(nil)

// BlobStoreArgs configures a Vercel BlobStore component with custom-env token propagation and prefix overlays.
type BlobStoreArgs struct {
	core.StatefulResourceArgs
	Shared                   *Shared
	SharedStackRef           string
	StoreID                  pulumi.StringInput
	VercelProjectID          pulumi.StringInput
	TeamID                   string
	Access                   string
	Region                   string
	StageCustomEnvironmentID pulumi.StringInput
	BlobReadWriteToken       pulumi.StringInput
}

// BlobStore provisions or attaches a Vercel Blob store with tier-scoped BLOB_PREFIX and BLOB_FALLBACK_PREFIX.
type BlobStore struct {
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

// NewBlobStore registers a Vercel BlobStore satisfying core.StatefulResource.
func NewBlobStore(ctx *pulumi.Context, name string, args BlobStoreArgs, opts ...pulumi.ResourceOption) (*BlobStore, error) {
	res := &BlobStore{}
	meta, err := core.InitStateful(ctx, "openbase:vercel:BlobStore", name, "vercel-blob", res, &res.StatefulMeta, args.StatefulResourceArgs, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionBlobStore(ctx, name, meta, args, res); err != nil {
		return nil, err
	}
	res.SetLink(buildBlobStoreLink(meta, args, res))
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
	args BlobStoreArgs,
	res *BlobStore,
) error {
	refs, err := resolveBlobRefs(ctx, name, args)
	if err != nil {
		return err
	}
	res.BlobPrefix = meta.Environment.String() + "/"
	if meta.BranchFrom != "" {
		res.BlobFallbackPrefix = meta.BranchFrom.String() + "/"
	}
	if err := ensureUnderlyingBlobStore(ctx, name, meta, args, &refs, res); err != nil {
		return err
	}
	res.StoreID = refs.storeID
	res.BlobBaseURL = DeriveBlobBaseURL(refs.storeID)
	return propagateStageBlobEnv(ctx, name, meta, args, refs, res)
}

func resolveBlobRefs(ctx *pulumi.Context, name string, args BlobStoreArgs) (resolvedBlobRefs, error) {
	var refs resolvedBlobRefs
	if args.StoreID != nil {
		refs.storeID = args.StoreID.ToStringOutput()
	}
	if args.VercelProjectID != nil {
		refs.projectID = args.VercelProjectID.ToStringOutput()
	}
	if args.StageCustomEnvironmentID != nil {
		refs.stageEnvID = args.StageCustomEnvironmentID.ToStringOutput()
	}
	applySharedBlobStruct(&refs, args.Shared)
	if args.SharedStackRef == "" {
		return refs, nil
	}
	return loadSharedBlobStackRef(ctx, name, args.SharedStackRef, refs)
}

func applySharedBlobStruct(refs *resolvedBlobRefs, shared *Shared) {
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
		refs.storeID = sr.GetStringOutput(pulumi.String("blobStoreId"))
	}
	if !isOutSet(refs.projectID) {
		refs.projectID = sr.GetStringOutput(pulumi.String("projectId"))
	}
	if !isOutSet(refs.stageEnvID) {
		refs.stageEnvID = sr.GetStringOutput(pulumi.String("stageCustomEnvironmentId"))
	}
	return refs, nil
}

func ensureUnderlyingBlobStore(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args BlobStoreArgs,
	refs *resolvedBlobRefs,
	res *BlobStore,
) error {
	if isOutSet(refs.storeID) {
		return nil
	}
	store, err := vercelprov.NewBlobStore(ctx, name+"-store", &vercelprov.BlobStoreArgs{
		Name:   pulumi.String(core.BoundedName(meta.CanonicalName, 52)),
		Access: pulumi.String(defaultVercelStr(args.Access, "public")),
		Region: pulumi.String(defaultVercelStr(args.Region, "iad1")),
		TeamId: optionalVercelStr(args.TeamID),
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
		TeamId:       optionalVercelStr(args.TeamID),
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
	args BlobStoreArgs,
	refs resolvedBlobRefs,
	res *BlobStore,
) error {
	if meta.Environment != core.EnvStage || !isOutSet(refs.projectID) || !isOutSet(refs.stageEnvID) {
		return nil
	}
	baseVar, err := vercelprov.NewProjectEnvironmentVariable(ctx, name+"-stage-base-url", &vercelprov.ProjectEnvironmentVariableArgs{
		ProjectId:            refs.projectID,
		TeamId:               optionalVercelStr(args.TeamID),
		CustomEnvironmentIds: pulumi.StringArray{refs.stageEnvID},
		Key:                  pulumi.String("BLOB_BASE_URL"),
		Value:                res.BlobBaseURL,
		Sensitive:            pulumi.Bool(false),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.StageBaseURLEnvVar = baseVar
	if args.BlobReadWriteToken == nil {
		return nil
	}
	tokVar, err := vercelprov.NewProjectEnvironmentVariable(ctx, name+"-stage-rw-token", &vercelprov.ProjectEnvironmentVariableArgs{
		ProjectId:            refs.projectID,
		TeamId:               optionalVercelStr(args.TeamID),
		CustomEnvironmentIds: pulumi.StringArray{refs.stageEnvID},
		Key:                  pulumi.String("BLOB_READ_WRITE_TOKEN"),
		Value:                pulumi.ToSecret(args.BlobReadWriteToken.ToStringOutput()).(pulumi.StringOutput),
		Sensitive:            pulumi.Bool(true),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.StageTokenEnvVar = tokVar
	return nil
}

func buildBlobStoreLink(meta core.ResourceMeta, args BlobStoreArgs, res *BlobStore) core.LinkDescriptor {
	envMap := map[string]pulumi.StringOutput{
		"BLOB_STORE_ID": res.StoreID,
		"BLOB_BASE_URL": res.BlobBaseURL,
		"BLOB_PREFIX":   pulumi.String(res.BlobPrefix).ToStringOutput(),
	}
	if res.BlobFallbackPrefix != "" {
		envMap["BLOB_FALLBACK_PREFIX"] = pulumi.String(res.BlobFallbackPrefix).ToStringOutput()
	}
	for k, v := range args.Env {
		envMap[k] = v.ToStringOutput()
	}
	secrets := map[string]pulumi.StringOutput{}
	if args.BlobReadWriteToken != nil {
		secrets["BLOB_READ_WRITE_TOKEN"] = pulumi.ToSecret(args.BlobReadWriteToken.ToStringOutput()).(pulumi.StringOutput)
	}
	for k, v := range args.Secrets {
		secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return core.LinkDescriptor{
		Name:    meta.Name,
		Kind:    meta.Kind,
		Env:     envMap,
		Secrets: secrets,
	}
}

func registerBlobOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *BlobStore) error {
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
