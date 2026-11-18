package composite

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*GcpCloudRunNeonAppResource)(nil)

const defaultGCPLocation = "us-central1"

// GcpCloudRunNeonApp declares a per-environment Cloud Run v2 web application (dev, stage, prod, preview-*) wired to GcpCloudRunNeonShared via StackReference.
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
	DisableBucket    bool
	BucketNamespace  string
	Image            pulumi.StringInput
	Command          []string
	ContainerArgs    []string
	Visibility       string
	Port             int
	Domain           string
	DatabaseName     string
	RoleName         string
	Auth             *KindeAppConfig
	SkipStackExports bool
}

// GcpCloudRunNeonAppResource holds the environment-scoped GCS bucket, Neon branch, Kinde binding, and Cloud Run service.
type GcpCloudRunNeonAppResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	Bucket      *gcp.BucketResource
	Database    *neon.DatabaseResource
	Auth        *kinde.OidcAppResource
	Service     *gcp.CloudRunServiceResource
	URL         pulumi.StringOutput
	RegionalURL pulumi.StringOutput
	DatabaseURL pulumi.StringOutput
}

// Register provisions per-environment Cloud Run resources on dev, stage, prod, or preview-* stacks.
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
	if err := requireNonSharedEnv(meta, "composite.GcpCloudRunNeonShared"); err != nil {
		return nil, err
	}
	if err := provisionGCPEnvStack(ctx, meta, g, res); err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"url":           res.URL,
		"regionalUrl":   res.RegionalURL,
	})
}

// Private helpers ------------------------------------------------------------

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
