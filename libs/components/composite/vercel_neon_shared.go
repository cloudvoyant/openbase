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

var _ core.StatelessResource = (*VercelNeonSharedResource)(nil)

// VercelNeonShared declares cross-environment Vercel, Neon, and Kinde platform roots in Premise's infra/shared program.
type VercelNeonShared struct {
	Name                 string
	Project              string
	Repo                 string
	Environment          string
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
	Auth                 *KindeSharedConfig
	SkipStackExports     bool
}

// VercelNeonSharedResource holds the shared Vercel Project, Neon Project, and Kinde OIDC roots exported for environment stacks.
type VercelNeonSharedResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	Vercel      *vercel.SharedResource
	Neon        *neon.SharedProjectResource
	Kinde       *kinde.SharedOidcAppResource
	ProdURL     pulumi.StringOutput
	BlobBaseURL pulumi.StringOutput
}

// Register provisions cross-environment Vercel, Neon, and Kinde roots on the "shared" stack.
func (v VercelNeonShared) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*VercelNeonSharedResource, error) {
	v.Name = defaultCompStr(v.Name, "platform")
	env := defaultCompStr(v.Environment, string(core.EnvShared))
	res := &VercelNeonSharedResource{}
	meta, err := core.InitStateless(ctx, "openbase:composite:VercelNeonShared", "composite-vercel-neon-shared", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        v.Name,
		Project:     v.Project,
		Repo:        v.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := requireSharedEnv(meta); err != nil {
		return nil, err
	}
	if err := provisionVercelSharedStack(ctx, meta, v, res); err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"prodUrl":       res.ProdURL,
	})
}

// Private helpers ------------------------------------------------------------

func provisionVercelSharedStack(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec VercelNeonShared,
	res *VercelNeonSharedResource,
) error {
	vProjName := defaultCompStr(spec.VercelProjectName, "openbase-"+meta.Project)
	vShared, err := vercel.Shared{
		Name:                 "vercel",
		Project:              meta.Project,
		Repo:                 meta.Repo,
		Environment:          string(meta.Environment),
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
	if err := registerSharedPreviewEnvVars(ctx, spec, vShared, kShared, res); err != nil {
		return err
	}
	res.Vercel = vShared
	res.Neon = nShared
	res.Kinde = kShared
	res.ProdURL = pulumi.Sprintf("https://%s", vShared.ProdDomainName)
	res.BlobBaseURL = vShared.BlobBaseURL
	res.SetLink(vShared.Link())
	exportVercelSharedOutputs(ctx, spec.SkipStackExports, vShared, nShared, kShared)
	return nil
}

func registerOptionalVercelSharedKinde(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	vProjName string,
	spec VercelNeonShared,
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

func registerSharedPreviewEnvVars(
	ctx *pulumi.Context,
	spec VercelNeonShared,
	v *vercel.SharedResource,
	k *kinde.SharedOidcAppResource,
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
	var teamPtr pulumi.StringPtrInput
	if spec.TeamID != "" {
		teamPtr = pulumi.String(spec.TeamID)
	}
	for _, e := range entries {
		_, err := vercelprov.NewProjectEnvironmentVariable(ctx, "shared-preview-"+e.slug, &vercelprov.ProjectEnvironmentVariableArgs{
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
	exportKindeSharedOutputs(ctx, k)
}
