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

var _ core.StatelessResource = (*GcpCloudRunNeonSharedResource)(nil)

// GcpCloudRunNeonShared declares cross-environment GCP, Neon, and Kinde platform roots in Premise's infra/shared program.
type GcpCloudRunNeonShared struct {
	Name             string
	ServiceName      string
	Project          string
	Repo             string
	Environment      string
	GCPProject       string
	GCPProjectNumber string
	Location         string
	PoolSuffix       string
	GitHubOrg        string
	GitHubRepo       string
	SkipKeyRing      bool
	NeonOrgID        string
	NeonRegionID     string
	Auth             *KindeSharedConfig
	SkipStackExports bool
}

// GcpCloudRunNeonSharedResource holds the shared GCP Artifact Registry/WIF, Neon Project, and Kinde OIDC roots.
type GcpCloudRunNeonSharedResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	GCP           *gcp.SharedResource
	Neon          *neon.SharedProjectResource
	Kinde         *kinde.SharedOidcAppResource
	RepositoryURL pulumi.StringOutput
}

// Register provisions cross-environment GCP, Neon, and Kinde roots on the "shared" stack.
func (g GcpCloudRunNeonShared) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*GcpCloudRunNeonSharedResource, error) {
	g.Name = defaultCompStr(g.Name, "platform")
	g.ServiceName = defaultCompStr(g.ServiceName, "web")
	g.Location = defaultCompStr(g.Location, defaultGCPLocation)
	env := defaultCompStr(g.Environment, string(core.EnvShared))
	res := &GcpCloudRunNeonSharedResource{}
	meta, err := core.InitStateless(ctx, "openbase:composite:GcpCloudRunNeonShared", "composite-gcp-cloud-run-neon-shared", res, &res.StatelessMeta, core.StatelessSpec{
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
	if err := provisionGCPSharedStack(ctx, meta, g, res); err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"repositoryUrl": res.RepositoryURL,
	})
}

// Private helpers ------------------------------------------------------------

func provisionGCPSharedStack(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec GcpCloudRunNeonShared,
	res *GcpCloudRunNeonSharedResource,
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
	res.GCP = gShared
	res.Neon = nShared
	res.Kinde = kShared
	res.RepositoryURL = gShared.RepositoryURL
	res.SetLink(gShared.Link())
	exportGCPSharedOutputs(ctx, spec.SkipStackExports, gShared, nShared, kShared)
	return nil
}

func registerOptionalGCPSharedKinde(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec GcpCloudRunNeonShared,
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
	stageHost, prodHost, devRedirects, devLogouts, prodExtrasR, prodExtrasL, err := computeGCPKindeURLs(meta.Project, spec, cb)
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
	spec GcpCloudRunNeonShared,
	cb string,
) (string, string, []string, []string, []string, []string, error) {
	stageURL, err := gcp.CloudRunURL(project, core.EnvStage, spec.ServiceName, spec.GCPProjectNumber, spec.Location)
	if err != nil {
		return "", "", nil, nil, nil, nil, err
	}
	prodURL, err := gcp.CloudRunURL(project, core.EnvProd, spec.ServiceName, spec.GCPProjectNumber, spec.Location)
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
	return strings.TrimPrefix(stageURL, "https://"), strings.TrimPrefix(prodURL, "https://"), devRedirects, devLogouts, prodExtrasR, prodExtrasL, nil
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
