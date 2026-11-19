package composite

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	vercelprov "github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

var _ core.ProjectResource = (*VercelNeonProjectResource)(nil)

// VercelNeonProject declares cross-environment Vercel, Neon, and Kinde project assets in Premise's infra/shared program.
type VercelNeonProject struct {
	Name                 string
	Project              string
	Repo                 string
	Platform             *PlatformResource
	PlatformStackRef     string
	VercelProjectName    string
	TeamID               string
	TeamSlug             string
	BaseDomain           string
	EnableDevEnvironment bool
	SkipStageEnvironment bool
	DevDomain            string
	ProdDomain           string
	StageDomain          string
	ExtraDevDomains      []string
	ExtraStageDomains    []string
	ExtraProdDomains     []string
	DisableBlobStore     bool
	NeonOrgID            string
	NeonRegionID         string
	Auth                 *KindeProjectConfig
	SkipStackExports     bool
}

// VercelNeonProjectResource holds the application's Vercel Project, Neon Project, and Kinde OIDC applications exported for environment stacks.
type VercelNeonProjectResource struct {
	pulumi.ResourceState
	core.ProjectMeta

	Vercel      *vercel.ProjectResource
	Neon        *neon.ProjectResource
	Kinde       *kinde.ProjectResource
	ProdURL     pulumi.StringOutput
	BlobBaseURL pulumi.StringOutput
}

// Register provisions cross-environment Vercel, Neon, and Kinde project assets on Premise's "shared" stack.
func (v VercelNeonProject) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*VercelNeonProjectResource, error) {
	v.Name = defaultCompStr(v.Name, "project")
	if err := core.RequireProjectStack(ctx, "composite-vercel-neon-project"); err != nil {
		return nil, err
	}
	res := &VercelNeonProjectResource{}
	info, err := core.InitProject(ctx, "openbase:composite:VercelNeonProject", "composite-vercel-neon-project", res, &res.ProjectMeta, core.ProjectSpec{
		Name:    v.Name,
		Project: v.Project,
		Repo:    v.Repo,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionVercelProjectStack(ctx, info, v, res); err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(info.CanonicalName),
		"prodUrl":       res.ProdURL,
	})
}

// Private helpers ------------------------------------------------------------

func provisionVercelProjectStack(
	ctx *pulumi.Context,
	info core.ProjectInfo,
	spec VercelNeonProject,
	res *VercelNeonProjectResource,
) error {
	vProjName := defaultCompStr(spec.VercelProjectName, "openbase-"+info.Project)
	vProj, err := vercel.Project{
		Name:                 "vercel",
		Project:              info.Project,
		Repo:                 info.Repo,
		Platform:             vercelPlatformFromComposite(spec.Platform),
		PlatformStackRef:     spec.PlatformStackRef,
		VercelProjectName:    vProjName,
		TeamID:               spec.TeamID,
		TeamSlug:             spec.TeamSlug,
		BaseDomain:           spec.BaseDomain,
		EnableDevEnvironment: spec.EnableDevEnvironment,
		SkipStageEnvironment: spec.SkipStageEnvironment,
		DevDomain:            spec.DevDomain,
		ProdDomain:           spec.ProdDomain,
		StageDomain:          spec.StageDomain,
		ExtraDevDomains:      spec.ExtraDevDomains,
		ExtraStageDomains:    spec.ExtraStageDomains,
		ExtraProdDomains:     spec.ExtraProdDomains,
		EnableBlobStore:      !spec.DisableBlobStore,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	nProj, err := neon.Project{
		Name:             "neon",
		Project:          info.Project,
		Repo:             info.Repo,
		Platform:         neonPlatformFromComposite(spec.Platform),
		PlatformStackRef: spec.PlatformStackRef,
		OrgID:            spec.NeonOrgID,
		RegionID:         spec.NeonRegionID,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	kProj, err := registerOptionalVercelProjectKinde(ctx, info, vProjName, spec, res)
	if err != nil {
		return err
	}
	if err := registerProjectPreviewEnvVars(ctx, spec, vProj, kProj, res); err != nil {
		return err
	}
	res.Vercel = vProj
	res.Neon = nProj
	res.Kinde = kProj
	res.ProdURL = pulumi.Sprintf("https://%s", vProj.ProdDomainName)
	res.BlobBaseURL = vProj.BlobBaseURL
	res.SetLink(vProj.Link())
	exportVercelProjectOutputs(ctx, spec.SkipStackExports, vProj, nProj, kProj)
	return nil
}

func vercelPlatformFromComposite(p *PlatformResource) *vercel.PlatformResource {
	if p == nil {
		return nil
	}
	return p.Vercel
}

func neonPlatformFromComposite(p *PlatformResource) *neon.PlatformResource {
	if p == nil {
		return nil
	}
	return p.Neon
}

func registerOptionalVercelProjectKinde(
	ctx *pulumi.Context,
	info core.ProjectInfo,
	vProjName string,
	spec VercelNeonProject,
	parent pulumi.Resource,
) (*kinde.ProjectResource, error) {
	if spec.Auth == nil {
		return nil, nil
	}
	cfg := spec.Auth
	return kinde.Project{
		Name:                       "kinde",
		Project:                    info.Project,
		Repo:                       info.Repo,
		Platform:                   resolveKindePlatform(spec.Platform, cfg),
		PlatformStackRef:           spec.PlatformStackRef,
		AppName:                    defaultCompStr(cfg.AppName, vProjName),
		VercelProjectName:          vProjName,
		VercelTeamSlug:             spec.TeamSlug,
		BaseDomain:                 spec.BaseDomain,
		DevDomain:                  spec.DevDomain,
		ProdDomain:                 spec.ProdDomain,
		StageDomain:                spec.StageDomain,
		ExtraDevDomains:            spec.ExtraDevDomains,
		ExtraStageDomains:          spec.ExtraStageDomains,
		ExtraProdDomains:           spec.ExtraProdDomains,
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

func registerProjectPreviewEnvVars(
	ctx *pulumi.Context,
	spec VercelNeonProject,
	v *vercel.ProjectResource,
	k *kinde.ProjectResource,
	parent pulumi.Resource,
) error {
	type previewEntry struct {
		slug      string
		key       string
		val       pulumi.StringOutput
		sensitive bool
	}
	entries := []previewEntry{
		{slug: "bypass", key: "VERCEL_AUTOMATION_BYPASS_SECRET", val: v.ProtectionBypassSecret, sensitive: true},
	}
	if !spec.DisableBlobStore {
		entries = append(entries, previewEntry{slug: "blob-url", key: "BLOB_BASE_URL", val: v.BlobBaseURL, sensitive: false})
	}
	if k != nil {
		entries = append(
			entries,
			previewEntry{slug: "kinde-issuer", key: "KINDE_ISSUER_URL", val: k.DevIssuerURL, sensitive: false},
			previewEntry{slug: "kinde-cid", key: "KINDE_CLIENT_ID", val: k.DevClientID, sensitive: false},
			previewEntry{slug: "kinde-csec", key: "KINDE_CLIENT_SECRET", val: k.DevClientSecret, sensitive: true},
		)
	}
	teamPtr := optionalTeamPtr(v.TeamID)
	for _, e := range entries {
		_, err := vercelprov.NewProjectEnvironmentVariable(ctx, "project-preview-"+e.slug, &vercelprov.ProjectEnvironmentVariableArgs{
			ProjectId: v.ProjectID,
			TeamId:    teamPtr,
			Targets:   pulumi.ToStringArray([]string{"preview"}),
			Key:       pulumi.String(e.key),
			Value:     e.val,
			Sensitive: pulumi.Bool(e.sensitive),
		}, pulumi.Parent(parent))
		if err != nil {
			return err
		}
	}
	return nil
}

func optionalTeamPtr(teamID pulumi.StringOutput) pulumi.StringPtrOutput {
	return teamID.ApplyT(func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}).(pulumi.StringPtrOutput)
}

func exportVercelProjectOutputs(
	ctx *pulumi.Context,
	skip bool,
	v *vercel.ProjectResource,
	n *neon.ProjectResource,
	k *kinde.ProjectResource,
) {
	if skip {
		return
	}
	ctx.Export("vercelProjectId", v.ProjectID)
	ctx.Export("vercelProjectName", v.VercelProjectName)
	ctx.Export("teamSlug", v.TeamSlug)
	ctx.Export("baseDomain", v.BaseDomain)
	ctx.Export("devCustomEnvironmentId", v.DevCustomEnvironmentID)
	ctx.Export("stageCustomEnvironmentId", v.StageCustomEnvironmentID)
	ctx.Export("protectionBypassSecret", v.ProtectionBypassSecret)
	ctx.Export("devDomainName", v.DevDomainName)
	ctx.Export("prodDomainName", v.ProdDomainName)
	ctx.Export("stageDomainName", v.StageDomainName)
	ctx.Export("blobStoreId", v.BlobStoreID)
	ctx.Export("blobBaseUrl", v.BlobBaseURL)
	ctx.Export("neonProjectId", n.ProjectID)
	ctx.Export("neonDefaultBranchId", n.DefaultBranchID)
	exportKindeProjectOutputs(ctx, k)
}
