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

// VercelNeonApp declares a Neokit-style Vercel web application backed by Neon PostgreSQL, Vercel Blob, and optional Kinde OIDC.
type VercelNeonApp struct {
	Name               string
	Project            string
	Repo               string
	Environment        string
	Links              []core.Resource
	Env                map[string]pulumi.StringInput
	Secrets            map[string]pulumi.StringInput
	SharedStackRef     string
	VercelProjectName  string
	TeamID             string
	TeamSlug           string
	SourceDir          string
	PathPrefix         string
	Framework          string
	RootDirectory      string
	ProdDomain         string
	StageDomain        string
	PreviewAlias       string
	DisableBlobStore   bool
	BlobReadWriteToken pulumi.StringInput
	NeonOrgID          string
	NeonRegionID       string
	DatabaseName       string
	RoleName           string
	Auth               *KindeConfig
	SkipStackExports   bool
}

// VercelNeonAppResource holds the shared or environment-scoped resources provisioned for a VercelNeonApp.
type VercelNeonAppResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	VercelShared *vercel.SharedResource
	NeonShared   *neon.SharedProjectResource
	KindeShared  *kinde.SharedOidcAppResource
	Database     *neon.DatabaseResource
	BlobStore    *vercel.BlobStoreResource
	Auth         *kinde.OidcAppResource
	WebApp       *vercel.WebAppResource
	URL          pulumi.StringOutput
	DatabaseURL  pulumi.StringOutput
}

// Register provisions shared roots on the "shared" stack or per-environment resources wired via StackReference.
func (v VercelNeonApp) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*VercelNeonAppResource, error) {
	v.Name = defaultCompStr(v.Name, "web")
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
	if meta.Environment == core.EnvShared {
		err = provisionVercelSharedStack(ctx, meta, v, res)
	} else {
		err = provisionVercelEnvStack(ctx, meta, v, res)
	}
	if err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"url":           res.URL,
	})
}

// Private helpers ------------------------------------------------------------

func provisionVercelSharedStack(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec VercelNeonApp,
	res *VercelNeonAppResource,
) error {
	vProjName := defaultCompStr(spec.VercelProjectName, "openbase-"+meta.Project)
	vShared, err := vercel.Shared{
		Name:              "vercel",
		Project:           meta.Project,
		Repo:              meta.Repo,
		Environment:       string(meta.Environment),
		VercelProjectName: vProjName,
		TeamID:            spec.TeamID,
		TeamSlug:          spec.TeamSlug,
		ProdDomain:        spec.ProdDomain,
		StageDomain:       spec.StageDomain,
		EnableBlobStore:   !spec.DisableBlobStore,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	nShared, err := neon.SharedProject{
		Name:        "neon",
		Project:     meta.Project,
		Repo:        meta.Repo,
		Environment: string(meta.Environment),
		OrgID:       spec.NeonOrgID,
		RegionID:    defaultCompStr(spec.NeonRegionID, "aws-us-east-1"),
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	kShared, err := registerOptionalVercelSharedKinde(ctx, meta, vProjName, spec, res)
	if err != nil {
		return err
	}
	res.VercelShared = vShared
	res.NeonShared = nShared
	res.KindeShared = kShared
	res.URL = pulumi.Sprintf("https://%s", vShared.ProdDomainName)
	res.SetLink(vShared.Link())
	exportVercelSharedOutputs(ctx, spec.SkipStackExports, vShared, nShared, kShared)
	return nil
}

func registerOptionalVercelSharedKinde(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	vProjName string,
	spec VercelNeonApp,
	parent pulumi.Resource,
) (*kinde.SharedOidcAppResource, error) {
	if spec.Auth == nil {
		return nil, nil
	}
	cfg := spec.Auth
	return kinde.SharedOidcApp{
		Name:                       "kinde",
		Project:                    meta.Project,
		Repo:                       meta.Repo,
		Environment:                string(meta.Environment),
		AppName:                    defaultCompStr(cfg.AppName, vProjName),
		VercelProjectName:          vProjName,
		VercelTeamSlug:             spec.TeamSlug,
		ProdDomain:                 spec.ProdDomain,
		StageDomain:                spec.StageDomain,
		CallbackPath:               cfg.CallbackPath,
		ExtraDevRedirectURIs:       cfg.ExtraDevRedirectURIs,
		ExtraDevLogoutURIs:         cfg.ExtraDevLogoutURIs,
		ExtraProdRedirectURIs:      cfg.ExtraProdRedirectURIs,
		ExtraProdLogoutURIs:        cfg.ExtraProdLogoutURIs,
		ImportDevAppID:             cfg.ImportDevAppID,
		ImportProdAppID:            cfg.ImportProdAppID,
		RetainApplicationsOnDelete: cfg.RetainApplicationsOnDelete,
		SkipEmailPasswordConn:      cfg.SkipEmailPasswordConn,
		Dev:                        cfg.Dev,
		Prod:                       cfg.Prod,
	}.Register(ctx, pulumi.Parent(parent))
}

func exportVercelSharedOutputs(
	ctx *pulumi.Context,
	skip bool,
	v *vercel.SharedResource,
	n *neon.SharedProjectResource,
	k *kinde.SharedOidcAppResource,
) {
	if skip {
		return
	}
	ctx.Export("vercelProjectId", v.ProjectID)
	ctx.Export("vercelProjectName", v.VercelProjectName)
	ctx.Export("teamSlug", v.TeamSlug)
	ctx.Export("stageCustomEnvironmentId", v.StageCustomEnvironmentID)
	ctx.Export("protectionBypassSecret", v.ProtectionBypassSecret)
	ctx.Export("prodDomainName", v.ProdDomainName)
	ctx.Export("stageDomainName", v.StageDomainName)
	ctx.Export("blobStoreId", v.BlobStoreID)
	ctx.Export("blobBaseUrl", v.BlobBaseURL)
	ctx.Export("neonProjectId", n.ProjectID)
	ctx.Export("neonDefaultBranchId", n.DefaultBranchID)
	exportKindeSharedOutputs(ctx, k)
}

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
		Name:              spec.Name,
		Project:           meta.Project,
		Repo:              meta.Repo,
		Environment:       string(meta.Environment),
		Links:             links,
		Env:               spec.Env,
		Secrets:           spec.Secrets,
		SharedStackRef:    sharedRef,
		VercelProjectName: spec.VercelProjectName,
		TeamID:            spec.TeamID,
		TeamSlug:          spec.TeamSlug,
		SourceDir:         spec.SourceDir,
		PathPrefix:        spec.PathPrefix,
		Framework:         spec.Framework,
		RootDirectory:     spec.RootDirectory,
		ProdDomain:        spec.ProdDomain,
		StageDomain:       spec.StageDomain,
		PreviewAlias:      spec.PreviewAlias,
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
		Name:               "uploads",
		Project:            meta.Project,
		Repo:               meta.Repo,
		Environment:        string(meta.Environment),
		BranchFrom:         blobBranch,
		SharedStackRef:     sharedRef,
		TeamID:             spec.TeamID,
		BlobReadWriteToken: spec.BlobReadWriteToken,
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
