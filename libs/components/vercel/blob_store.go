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
	ProjectName              string
	Repo                     string
	Environment              string
	Kind                     string
	BranchFrom               string
	Backup                   *bool
	Links                    []core.Resource
	Project                  ProjectInput
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

// BlobStore is the provisioned Vercel Blob store handle with tier-scoped BLOB_PREFIX and BLOB_FALLBACK_PREFIX.
type BlobStore struct {
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

// NewBlobStore provisions or attaches a Vercel BlobStore satisfying core.StatefulResource.
func NewBlobStore(ctx *pulumi.Context, name string, args *BlobStoreArgs, opts ...pulumi.ResourceOption) (*BlobStore, error) {
	if args == nil {
		args = &BlobStoreArgs{}
	}
	spec := *args
	if env, err := core.ResolveEnvironment(ctx, spec.Environment); err == nil {
		spec.BranchFrom = core.DefaultBranchFrom(env, spec.BranchFrom, false)
	}
	res := &BlobStore{}
	meta, err := core.InitStateful(ctx, "openbase:vercel:BlobStore", "vercel-blob", res, &res.StatefulMeta, core.StatefulSpec{
		Name:        name,
		Project:     spec.ProjectName,
		Repo:        spec.Repo,
		Environment: spec.Environment,
		Kind:        spec.Kind,
		BranchFrom:  spec.BranchFrom,
		Backup:      spec.Backup,
		Links:       spec.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionBlobStore(ctx, name, meta, spec, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, buildBlobStoreLink(meta, res))
	return res, registerBlobOutputs(ctx, meta, res)
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
	spec BlobStoreArgs,
	res *BlobStore,
) error {
	refs := resolveBlobRefs(spec)
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

func resolveBlobRefs(spec BlobStoreArgs) resolvedBlobRefs {
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
	applyVercelProjectBlobInput(&refs, spec.Project)
	return refs
}

func applyVercelProjectBlobInput(refs *resolvedBlobRefs, proj ProjectInput) {
	if proj == nil {
		return
	}
	pv := proj.vercelProjectValues()
	if !isOutSet(refs.storeID) && isOutSet(pv.blobStoreID) {
		refs.storeID = pv.blobStoreID
	}
	if !isOutSet(refs.projectID) && isOutSet(pv.projectID) {
		refs.projectID = pv.projectID
	}
	if !isOutSet(refs.devEnvID) && isOutSet(pv.devCustomEnvironmentID) {
		refs.devEnvID = pv.devCustomEnvironmentID
	}
	if !isOutSet(refs.stageEnvID) && isOutSet(pv.stageCustomEnvironmentID) {
		refs.stageEnvID = pv.stageCustomEnvironmentID
	}
}

func ensureUnderlyingBlobStore(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec BlobStoreArgs,
	refs *resolvedBlobRefs,
	res *BlobStore,
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
	spec BlobStoreArgs,
	refs resolvedBlobRefs,
	res *BlobStore,
) error {
	hasDevCustom := spec.EnableDevEnvironment || (spec.Project != nil && spec.Project.vercelProjectValues().hasDevCustomEnv)
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
	spec BlobStoreArgs,
	refs resolvedBlobRefs,
	res *BlobStore,
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
	spec BlobStoreArgs,
	res *BlobStore,
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

func buildBlobStoreLink(meta core.ResourceMeta, res *BlobStore) core.LinkDescriptor {
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
