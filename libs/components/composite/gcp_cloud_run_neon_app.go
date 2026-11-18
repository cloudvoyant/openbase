package composite

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*GcpCloudRunNeonAppResource)(nil)

const defaultGCPLocation = "us-central1"

// GcpCloudRunNeonApp declares a Cloud Run v2 web application backed by Neon PostgreSQL, GCS, and optional Kinde OIDC.
type GcpCloudRunNeonApp struct {
	Name             string
	Project          string
	Repo             string
	Environment      string
	Links            []core.Resource
	Env              map[string]pulumi.StringInput
	Secrets          map[string]pulumi.StringInput
	SharedStackRef   string
	GCPProject       string
	GCPProjectNumber string
	Location         string
	PoolSuffix       string
	GitHubOrg        string
	GitHubRepo       string
	SkipKeyRing      bool
	DisableBucket    bool
	BucketNamespace  string
	Image            pulumi.StringInput
	Command          []string
	ContainerArgs    []string
	Visibility       string
	Port             int
	Domain           string
	NeonOrgID        string
	NeonRegionID     string
	DatabaseName     string
	RoleName         string
	Auth             *KindeConfig
	SkipStackExports bool
}

// GcpCloudRunNeonAppResource holds the shared or environment-scoped resources provisioned for a GcpCloudRunNeonApp.
type GcpCloudRunNeonAppResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	GCPShared   *gcp.SharedResource
	NeonShared  *neon.SharedProjectResource
	KindeShared *kinde.SharedOidcAppResource
	Bucket      *gcp.BucketResource
	Database    *neon.DatabaseResource
	Auth        *kinde.OidcAppResource
	Service     *gcp.CloudRunServiceResource
	URL         pulumi.StringOutput
	RegionalURL pulumi.StringOutput
	DatabaseURL pulumi.StringOutput
}

// Register provisions shared roots on the "shared" stack or per-environment Cloud Run resources via StackReference.
func (g GcpCloudRunNeonApp) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*GcpCloudRunNeonAppResource, error) {
	g.Name = defaultCompStr(g.Name, "web")
	g.Location = defaultCompStr(g.Location, defaultGCPLocation)
	res := &GcpCloudRunNeonAppResource{}
	meta, err := core.InitStateless(ctx, "openbase:composite:GcpCloudRunNeonApp", "composite-gcp-cloud-run-neon-app", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        g.Name,
		Project:     g.Project,
		Repo:        g.Repo,
		Environment: g.Environment,
		Links:       g.Links,
		Env:         g.Env,
		Secrets:     g.Secrets,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if meta.Environment == core.EnvShared {
		err = provisionGCPSharedStack(ctx, meta, g, res)
	} else {
		err = provisionGCPEnvStack(ctx, meta, g, res)
	}
	if err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"url":           res.URL,
		"regionalUrl":   res.RegionalURL,
	})
}

// Private helpers ------------------------------------------------------------

func provisionGCPSharedStack(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec GcpCloudRunNeonApp,
	res *GcpCloudRunNeonAppResource,
) error {
	gShared, err := gcp.Shared{
		Name:        "gcp",
		Project:     meta.Project,
		Repo:        meta.Repo,
		Environment: string(meta.Environment),
		GCPProject:  optionalGCPProjectPtr(spec.GCPProject),
		Location:    spec.Location,
		PoolSuffix:  spec.PoolSuffix,
		GitHubOrg:   spec.GitHubOrg,
		GitHubRepo:  spec.GitHubRepo,
		SkipKeyRing: spec.SkipKeyRing,
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
	kShared, err := registerOptionalGCPSharedKinde(ctx, meta, spec, res)
	if err != nil {
		return err
	}
	res.GCPShared = gShared
	res.NeonShared = nShared
	res.KindeShared = kShared
	res.URL = gShared.RepositoryURL
	res.RegionalURL = gShared.RepositoryURL
	res.SetLink(gShared.Link())
	exportGCPSharedOutputs(ctx, spec.SkipStackExports, gShared, nShared, kShared)
	return nil
}

func registerOptionalGCPSharedKinde(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec GcpCloudRunNeonApp,
	parent pulumi.Resource,
) (*kinde.SharedOidcAppResource, error) {
	if spec.Auth == nil {
		return nil, nil
	}
	cfg := spec.Auth
	cb := defaultCompStr(cfg.CallbackPath, "/api/auth/kinde_callback")
	if !strings.HasPrefix(cb, "/") {
		cb = "/" + cb
	}
	stageHost, prodHost, devRedirects, devLogouts, err := computeGCPKindeURLs(meta.Project, spec, cb)
	if err != nil {
		return nil, err
	}
	return kinde.SharedOidcApp{
		Name:                       "kinde",
		Project:                    meta.Project,
		Repo:                       meta.Repo,
		Environment:                string(meta.Environment),
		AppName:                    defaultCompStr(cfg.AppName, "openbase-"+meta.Project),
		StageDomain:                stageHost,
		ProdDomain:                 prodHost,
		CallbackPath:               cb,
		ExtraDevRedirectURIs:       append(devRedirects, cfg.ExtraDevRedirectURIs...),
		ExtraDevLogoutURIs:         append(devLogouts, cfg.ExtraDevLogoutURIs...),
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

func computeGCPKindeURLs(project string, spec GcpCloudRunNeonApp, cb string) (string, string, []string, []string, error) {
	stageURL, err := gcp.CloudRunURL(project, core.EnvStage, spec.Name, spec.GCPProjectNumber, spec.Location)
	if err != nil {
		return "", "", nil, nil, err
	}
	prodURL, err := gcp.CloudRunURL(project, core.EnvProd, spec.Name, spec.GCPProjectNumber, spec.Location)
	if err != nil {
		return "", "", nil, nil, err
	}
	hashWildcard := fmt.Sprintf("https://%s-*-uc.a.run.app", project)
	devRedirects := []string{hashWildcard + cb}
	devLogouts := []string{hashWildcard + "/"}
	if spec.GCPProjectNumber != "" {
		regWildcard := fmt.Sprintf("https://%s-*-%s.%s.run.app", project, spec.GCPProjectNumber, spec.Location)
		devRedirects = append(devRedirects, regWildcard+cb)
		devLogouts = append(devLogouts, regWildcard+"/")
	}
	return strings.TrimPrefix(stageURL, "https://"), strings.TrimPrefix(prodURL, "https://"), devRedirects, devLogouts, nil
}

func exportGCPSharedOutputs(
	ctx *pulumi.Context,
	skip bool,
	g *gcp.SharedResource,
	n *neon.SharedProjectResource,
	k *kinde.SharedOidcAppResource,
) {
	if skip {
		return
	}
	ctx.Export("repositoryId", g.RepositoryID)
	ctx.Export("repositoryUrl", g.RepositoryURL)
	ctx.Export("workloadIdentityPoolName", g.WorkloadIdentityPoolName)
	ctx.Export("neonProjectId", n.ProjectID)
	ctx.Export("neonDefaultBranchId", n.DefaultBranchID)
	exportKindeSharedOutputs(ctx, k)
}

func provisionGCPEnvStack(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec GcpCloudRunNeonApp,
	res *GcpCloudRunNeonAppResource,
) error {
	sharedRef := resolveSharedStackRef(ctx, spec.SharedStackRef)
	regionalURL, err := gcp.CloudRunURL(meta.Project, meta.Environment, spec.Name, spec.GCPProjectNumber, spec.Location)
	if err != nil {
		return err
	}
	links, err := registerGCPBackingResources(ctx, meta, sharedRef, regionalURL, spec, res)
	if err != nil {
		return err
	}
	links = append(links, spec.Links...)
	envMap := mergeWithOpenBaseEnv(meta.Environment, spec.Env)

	svc, err := gcp.CloudRunService{
		Name:             spec.Name,
		Project:          meta.Project,
		Repo:             meta.Repo,
		Environment:      string(meta.Environment),
		Links:            links,
		Env:              envMap,
		Secrets:          spec.Secrets,
		GCPProject:       optionalGCPProjectPtr(spec.GCPProject),
		GCPProjectNumber: optionalStringInput(spec.GCPProjectNumber),
		SharedStackRef:   sharedRef,
		Location:         spec.Location,
		Image:            spec.Image,
		Command:          spec.Command,
		ContainerArgs:    spec.ContainerArgs,
		Visibility:       defaultCompStr(spec.Visibility, gcp.VisibilityPublic),
		Port:             spec.Port,
		Domain:           spec.Domain,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Service = svc
	res.URL = svc.URL
	res.RegionalURL = pulumi.String(regionalURL).ToStringOutput()
	res.DatabaseURL = res.Database.DatabaseURL
	res.SetLink(svc.Link())
	exportGCPEnvOutputs(ctx, spec.SkipStackExports, res)
	return nil
}

func registerGCPBackingResources(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	sharedRef, regionalURL string,
	spec GcpCloudRunNeonApp,
	res *GcpCloudRunNeonAppResource,
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

	if err := attachOptionalGCPBucket(ctx, meta, spec, res, &links); err != nil {
		return nil, err
	}
	if err := attachOptionalGCPKinde(ctx, meta, sharedRef, regionalURL, spec, res, &links); err != nil {
		return nil, err
	}
	return links, nil
}

func attachOptionalGCPBucket(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec GcpCloudRunNeonApp,
	res *GcpCloudRunNeonAppResource,
	links *[]core.Resource,
) error {
	if spec.DisableBucket {
		return nil
	}
	bucketBranch, bucketBranchRef := resolvePreviewStorageBranch(ctx, meta.Environment)
	ns := defaultCompStr(spec.BucketNamespace, spec.GCPProject)
	bkt, err := gcp.Bucket{
		Name:            "uploads",
		Project:         meta.Project,
		Repo:            meta.Repo,
		Environment:     string(meta.Environment),
		BranchFrom:      bucketBranch,
		BranchStackRef:  bucketBranchRef,
		GCPProject:      optionalGCPProjectPtr(spec.GCPProject),
		BucketNamespace: ns,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Bucket = bkt
	*links = append(*links, bkt)
	return nil
}

func attachOptionalGCPKinde(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	sharedRef, regionalURL string,
	spec GcpCloudRunNeonApp,
	res *GcpCloudRunNeonAppResource,
	links *[]core.Resource,
) error {
	if spec.Auth == nil {
		return nil
	}
	auth, err := kinde.OidcApp{
		Name:           "auth",
		Project:        meta.Project,
		Repo:           meta.Repo,
		Environment:    string(meta.Environment),
		SharedStackRef: sharedRef,
		SiteURL:        pulumi.String(regionalURL),
		CallbackPath:   spec.Auth.CallbackPath,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Auth = auth
	*links = append(*links, auth)
	return nil
}

func mergeWithOpenBaseEnv(env core.Environment, extra map[string]pulumi.StringInput) map[string]pulumi.StringInput {
	merged := map[string]pulumi.StringInput{
		"OPENBASE_ENVIRONMENT": pulumi.String(env.String()),
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}

func exportGCPEnvOutputs(ctx *pulumi.Context, skip bool, res *GcpCloudRunNeonAppResource) {
	if skip {
		return
	}
	ctx.Export("url", res.Service.URL)
	ctx.Export("regionalUrl", res.RegionalURL)
	ctx.Export("serviceName", res.Service.Service.Name)
	ctx.Export("serviceAccountEmail", res.Service.ServiceAccountEmail)
	ctx.Export("branchId", res.Database.BranchID)
	ctx.Export("databaseUrl", res.Database.DatabaseURL)
	ctx.Export("directUrl", res.Database.DirectURL)
	if res.Bucket != nil {
		ctx.Export("bucketName", res.Bucket.BucketName)
		ctx.Export("fallbackBucketName", res.Bucket.FallbackBucketName)
	}
	exportKindeEnvOutputs(ctx, res.Auth)
}

func optionalGCPProjectPtr(project string) pulumi.StringPtrInput {
	if project == "" {
		return nil
	}
	return pulumi.String(project)
}

func optionalStringInput(val string) pulumi.StringInput {
	if val == "" {
		return nil
	}
	return pulumi.String(val)
}
