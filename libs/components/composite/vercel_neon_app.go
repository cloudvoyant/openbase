package composite

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*VercelNeonAppResource)(nil)

// VercelNeonApp declares a per-environment Vercel web application (dev, stage, prod, preview-*) wired to VercelNeonShared via StackReference.
type VercelNeonApp struct {
	Name                 string
	Project              string
	Repo                 string
	Environment          string
	Links                []core.Resource
	Env                  map[string]pulumi.StringInput
	Secrets              map[string]pulumi.StringInput
	SharedStackRef       string
	VercelProjectName    string
	TeamID               string
	TeamSlug             string
	BaseDomain           string
	EnableDevEnvironment bool
	SkipStageEnvironment bool
	SourceDir            string
	PathPrefix           string
	Framework            string
	RootDirectory        string
	DevDomain            string
	ProdDomain           string
	StageDomain          string
	PreviewAlias         string
	ExtraDomains         []string
	DisableBlobStore     bool
	BlobReadWriteToken   pulumi.StringInput
	DatabaseName         string
	RoleName             string
	Auth                 *KindeAppConfig
	SkipStackExports     bool
}

// VercelNeonAppResource holds the environment-scoped Neon branch, Blob overlay, Kinde binding, and Vercel WebApp.
type VercelNeonAppResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	Database    *neon.DatabaseResource
	BlobStore   *vercel.BlobStoreResource
	Auth        *kinde.OidcAppResource
	WebApp      *vercel.WebAppResource
	URL         pulumi.StringOutput
	DatabaseURL pulumi.StringOutput
}

// Register provisions per-environment resources on dev, stage, prod, or preview-* stacks.
func (v VercelNeonApp) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*VercelNeonAppResource, error) {
	v.Name = defaultCompStr(v.Name, "web")
	if v.SourceDir != "" && v.SourceDir != "." && v.PathPrefix == "" {
		v.PathPrefix = v.SourceDir
	}
	res := &VercelNeonAppResource{}
	meta, err := core.InitStateless(ctx, "openbase:composite:VercelNeonApp", "composite-vercel-neon-app", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        v.Name,
		Project:     v.Project,
		Repo:        v.Repo,
		Environment: v.Environment,
		Links:       v.Links,
		Env:         v.Env,
		Secrets:     v.Secrets,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := requireNonSharedEnv(meta, "composite.VercelNeonShared"); err != nil {
		return nil, err
	}
	if err := provisionVercelEnvStack(ctx, meta, v, res); err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"url":           res.URL,
	})
}

// Private helpers ------------------------------------------------------------

func provisionVercelEnvStack(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec VercelNeonApp,
	res *VercelNeonAppResource,
) error {
	sharedRef := resolveSharedStackRef(ctx, spec.SharedStackRef)
	links, err := registerVercelBackingResources(ctx, meta, sharedRef, spec, res)
	if err != nil {
		return err
	}
	links = append(links, spec.Links...)
	web, err := vercel.WebApp{
		Name:                 spec.Name,
		Project:              meta.Project,
		Repo:                 meta.Repo,
		Environment:          string(meta.Environment),
		Links:                links,
		Env:                  spec.Env,
		Secrets:              spec.Secrets,
		SharedStackRef:       sharedRef,
		VercelProjectName:    spec.VercelProjectName,
		TeamID:               spec.TeamID,
		TeamSlug:             spec.TeamSlug,
		BaseDomain:           spec.BaseDomain,
		EnableDevEnvironment: spec.EnableDevEnvironment,
		SkipStageEnvironment: spec.SkipStageEnvironment,
		SourceDir:            spec.SourceDir,
		PathPrefix:           spec.PathPrefix,
		Framework:            spec.Framework,
		RootDirectory:        spec.RootDirectory,
		DevDomain:            spec.DevDomain,
		ProdDomain:           spec.ProdDomain,
		StageDomain:          spec.StageDomain,
		PreviewAlias:         spec.PreviewAlias,
		ExtraDomains:         spec.ExtraDomains,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.WebApp = web
	res.URL = web.URL
	res.DatabaseURL = res.Database.DatabaseURL
	res.SetLink(web.Link())
	exportVercelEnvOutputs(ctx, spec.SkipStackExports, res)
	return nil
}

func registerVercelBackingResources(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	sharedRef string,
	spec VercelNeonApp,
	res *VercelNeonAppResource,
) ([]core.Resource, error) {
	dbBranch, dbBranchRef := resolveDBBranch(ctx, meta.Environment)
	db, err := neon.Database{
		Name:           "db",
		Project:        meta.Project,
		Repo:           meta.Repo,
		Environment:    string(meta.Environment),
		BranchFrom:     dbBranch,
		SharedStackRef: sharedRef,
		BranchStackRef: dbBranchRef,
		DatabaseName:   spec.DatabaseName,
		RoleName:       spec.RoleName,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return nil, err
	}
	res.Database = db
	links := []core.Resource{db}

	if err := attachOptionalVercelBlob(ctx, meta, sharedRef, spec, res, &links); err != nil {
		return nil, err
	}
	if err := attachOptionalVercelKinde(ctx, meta, sharedRef, spec, res, &links); err != nil {
		return nil, err
	}
	return links, nil
}

func attachOptionalVercelBlob(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	sharedRef string,
	spec VercelNeonApp,
	res *VercelNeonAppResource,
	links *[]core.Resource,
) error {
	if spec.DisableBlobStore {
		return nil
	}
	blobBranch, _ := resolvePreviewStorageBranch(ctx, meta.Environment)
	blobs, err := vercel.BlobStore{
		Name:                 "uploads",
		Project:              meta.Project,
		Repo:                 meta.Repo,
		Environment:          string(meta.Environment),
		BranchFrom:           blobBranch,
		SharedStackRef:       sharedRef,
		TeamID:               spec.TeamID,
		EnableDevEnvironment: spec.EnableDevEnvironment,
		SkipStageEnvironment: spec.SkipStageEnvironment,
		BlobReadWriteToken:   spec.BlobReadWriteToken,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.BlobStore = blobs
	*links = append(*links, blobs)
	return nil
}

func attachOptionalVercelKinde(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	sharedRef string,
	spec VercelNeonApp,
	res *VercelNeonAppResource,
	links *[]core.Resource,
) error {
	if spec.Auth == nil {
		return nil
	}
	auth, err := kinde.OidcApp{
		Name:              "auth",
		Project:           meta.Project,
		Repo:              meta.Repo,
		Environment:       string(meta.Environment),
		SharedStackRef:    sharedRef,
		VercelProjectName: spec.VercelProjectName,
		VercelTeamSlug:    spec.TeamSlug,
		BaseDomain:        spec.BaseDomain,
		DevDomain:         spec.DevDomain,
		ProdDomain:        spec.ProdDomain,
		StageDomain:       spec.StageDomain,
		CallbackPath:      spec.Auth.CallbackPath,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Auth = auth
	*links = append(*links, auth)
	return nil
}

func exportVercelEnvOutputs(ctx *pulumi.Context, skip bool, res *VercelNeonAppResource) {
	if skip {
		return
	}
	ctx.Export("url", res.WebApp.URL)
	ctx.Export("urls", res.WebApp.URLs)
	ctx.Export("aliasHost", res.WebApp.AliasHost)
	ctx.Export("deploymentId", res.WebApp.DeploymentID)
	ctx.Export("branchId", res.Database.BranchID)
	ctx.Export("databaseUrl", res.Database.DatabaseURL)
	ctx.Export("directUrl", res.Database.DirectURL)
	if res.BlobStore != nil {
		ctx.Export("blobBaseUrl", res.BlobStore.BlobBaseURL)
		ctx.Export("blobPrefix", pulumi.String(res.BlobStore.BlobPrefix))
		ctx.Export("blobFallbackPrefix", pulumi.String(res.BlobStore.BlobFallbackPrefix))
	}
	exportKindeEnvOutputs(ctx, res.Auth)
}
