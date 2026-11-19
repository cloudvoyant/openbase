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

var _ core.StatelessResource = (*GcpCloudRunNeonProjectResource)(nil)

// GcpCloudRunNeonProject declares cross-environment GCP, Neon, and Kinde project assets in Premise's infra/shared program.
type GcpCloudRunNeonProject struct {
	Name               string
	ServiceName        string
	Project            string
	Repo               string
	Environment        string
	Platform           *PlatformResource
	PlatformStackRef   string
	GCPProject         string
	GCPProjectNumber   string
	Location           string
	BaseDomain         string
	EnableLoadBalancer bool
	ExtraDevDomains    []string
	ExtraStageDomains  []string
	ExtraProdDomains   []string
	PoolSuffix         string
	GitHubOrg          string
	SkipKeyRing        bool
	NeonOrgID          string
	NeonRegionID       string
	Auth               *KindeProjectConfig
	SkipStackExports   bool
}

// GcpCloudRunNeonProjectResource holds the application's GCP Artifact Registry, Neon Project, and Kinde OIDC applications.
type GcpCloudRunNeonProjectResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	GCP           *gcp.ProjectResource
	Neon          *neon.ProjectResource
	Kinde         *kinde.ProjectResource
	RepositoryURL pulumi.StringOutput
}

// Register provisions cross-environment GCP, Neon, and Kinde project assets on Premise's "shared" stack.
func (g GcpCloudRunNeonProject) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*GcpCloudRunNeonProjectResource, error) {
	g.Name = defaultCompStr(g.Name, "project")
	g.ServiceName = defaultCompStr(g.ServiceName, "web")
	g.Location = defaultCompStr(g.Location, defaultGCPLocation)
	env := defaultCompStr(g.Environment, string(core.EnvShared))
	res := &GcpCloudRunNeonProjectResource{}
	meta, err := core.InitStateless(ctx, "openbase:composite:GcpCloudRunNeonProject", "composite-gcp-cloud-run-neon-project", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        g.Name,
		Project:     g.Project,
		Repo:        g.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := requireSharedEnv(meta); err != nil {
		return nil, err
	}
	if err := provisionGCPProjectStack(ctx, meta, g, res); err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"repositoryUrl": res.RepositoryURL,
	})
}

// Private helpers ------------------------------------------------------------

func provisionGCPProjectStack(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec GcpCloudRunNeonProject,
	res *GcpCloudRunNeonProjectResource,
) error {
	gProj, err := gcp.Project{
		Name:               "gcp",
		Project:            meta.Project,
		Repo:               meta.Repo,
		Environment:        string(meta.Environment),
		Platform:           gcpPlatformFromComposite(spec.Platform),
		PlatformStackRef:   spec.PlatformStackRef,
		GCPProject:         optionalGCPProjectPtr(spec.GCPProject),
		Location:           spec.Location,
		BaseDomain:         spec.BaseDomain,
		EnableLoadBalancer: spec.EnableLoadBalancer,
		PoolSuffix:         spec.PoolSuffix,
		GitHubOrg:          spec.GitHubOrg,
		SkipKeyRing:        spec.SkipKeyRing,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	nProj, err := neon.Project{
		Name:             "neon",
		Project:          meta.Project,
		Repo:             meta.Repo,
		Environment:      string(meta.Environment),
		Platform:         neonPlatformFromComposite(spec.Platform),
		PlatformStackRef: spec.PlatformStackRef,
		OrgID:            spec.NeonOrgID,
		RegionID:         spec.NeonRegionID,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	kProj, err := registerOptionalGCPProjectKinde(ctx, meta, spec, res)
	if err != nil {
		return err
	}
	res.GCP = gProj
	res.Neon = nProj
	res.Kinde = kProj
	res.RepositoryURL = gProj.RepositoryURL
	res.SetLink(gProj.Link())
	exportGCPProjectOutputs(ctx, spec.SkipStackExports, gProj, nProj, kProj)
	return nil
}

func gcpPlatformFromComposite(p *PlatformResource) *gcp.PlatformResource {
	if p == nil {
		return nil
	}
	return p.GCP
}

func registerOptionalGCPProjectKinde(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec GcpCloudRunNeonProject,
	parent pulumi.Resource,
) (*kinde.ProjectResource, error) {
	if spec.Auth == nil {
		return nil, nil
	}
	cfg := spec.Auth
	cb := defaultCompStr(cfg.CallbackPath, "/api/auth/kinde_callback")
	if !strings.HasPrefix(cb, "/") {
		cb = "/" + cb
	}
	stageHost, prodHost, devRedirects, devLogouts, prodExtrasR, prodExtrasL, err := computeGCPKindeURLs(meta.Project, spec, cb)
	if err != nil {
		return nil, err
	}
	return kinde.Project{
		Name:                       "kinde",
		Project:                    meta.Project,
		Repo:                       meta.Repo,
		Environment:                string(meta.Environment),
		Platform:                   resolveKindePlatform(spec.Platform, cfg),
		PlatformStackRef:           spec.PlatformStackRef,
		AppName:                    defaultCompStr(cfg.AppName, "openbase-"+meta.Project),
		BaseDomain:                 spec.BaseDomain,
		StageDomain:                stageHost,
		ProdDomain:                 prodHost,
		ExtraDevDomains:            spec.ExtraDevDomains,
		ExtraStageDomains:          spec.ExtraStageDomains,
		ExtraProdDomains:           spec.ExtraProdDomains,
		CallbackPath:               cb,
		ExtraDevRedirectURIs:       append(devRedirects, cfg.ExtraDevRedirectURIs...),
		ExtraDevLogoutURIs:         append(devLogouts, cfg.ExtraDevLogoutURIs...),
		ExtraProdRedirectURIs:      append(prodExtrasR, cfg.ExtraProdRedirectURIs...),
		ExtraProdLogoutURIs:        append(prodExtrasL, cfg.ExtraProdLogoutURIs...),
		ImportDevAppID:             cfg.ImportDevAppID,
		ImportProdAppID:            cfg.ImportProdAppID,
		RetainApplicationsOnDelete: cfg.RetainApplicationsOnDelete,
		SkipEmailPasswordConn:      cfg.SkipEmailPasswordConn,
		Dev:                        cfg.Dev,
		Prod:                       cfg.Prod,
	}.Register(ctx, pulumi.Parent(parent))
}

func computeGCPKindeURLs(
	project string,
	spec GcpCloudRunNeonProject,
	cb string,
) (string, string, []string, []string, []string, []string, error) {
	stageURL, err := gcp.CloudRunRegionalURL(project, core.EnvStage, spec.ServiceName, spec.GCPProjectNumber, spec.Location, spec.BaseDomain)
	if err != nil {
		return "", "", nil, nil, nil, nil, err
	}
	prodURL, err := gcp.CloudRunRegionalURL(project, core.EnvProd, spec.ServiceName, spec.GCPProjectNumber, spec.Location, spec.BaseDomain)
	if err != nil {
		return "", "", nil, nil, nil, nil, err
	}
	hashWildcard := fmt.Sprintf("https://%s-*-uc.a.run.app", project)
	devRedirects := []string{hashWildcard + cb}
	devLogouts := []string{hashWildcard + "/"}
	if spec.GCPProjectNumber != "" {
		regWildcard := fmt.Sprintf("https://%s-*-%s.%s.run.app", project, spec.GCPProjectNumber, spec.Location)
		devRedirects = append(devRedirects, regWildcard+cb)
		devLogouts = append(devLogouts, regWildcard+"/")
	}
	stageHash := fmt.Sprintf("https://%s-stage-*-uc.a.run.app", project)
	prodHash := fmt.Sprintf("https://%s-prod-*-uc.a.run.app", project)
	prodExtrasR := []string{stageHash + cb, prodHash + cb}
	prodExtrasL := []string{stageHash + "/", prodHash + "/"}
	if core.NormalizeDomain(spec.BaseDomain) != "" {
		prodExtrasR = append(prodExtrasR, stageURL+cb, prodURL+cb)
		prodExtrasL = append(prodExtrasL, stageURL+"/", prodURL+"/")
		return "", "", devRedirects, devLogouts, prodExtrasR, prodExtrasL, nil
	}
	return strings.TrimPrefix(stageURL, "https://"), strings.TrimPrefix(prodURL, "https://"), devRedirects, devLogouts, prodExtrasR, prodExtrasL, nil
}

func exportGCPProjectOutputs(
	ctx *pulumi.Context,
	skip bool,
	g *gcp.ProjectResource,
	n *neon.ProjectResource,
	k *kinde.ProjectResource,
) {
	if skip {
		return
	}
	ctx.Export("baseDomain", g.BaseDomain)
	ctx.Export("repositoryId", g.RepositoryID)
	ctx.Export("repositoryUrl", g.RepositoryURL)
	ctx.Export("workloadIdentityPoolName", g.WorkloadIdentityPoolName)
	if g.LoadBalancer != nil {
		ctx.Export("loadBalancerIp", g.LoadBalancer.IPAddress)
		ctx.Export("dnsAuthRecordName", g.LoadBalancer.DnsAuthRecordName)
		ctx.Export("dnsAuthRecordData", g.LoadBalancer.DnsAuthRecordData)
	}
	ctx.Export("neonProjectId", n.ProjectID)
	ctx.Export("neonDefaultBranchId", n.DefaultBranchID)
	exportKindeProjectOutputs(ctx, k)
}
