// Package vercel provides OpenBase Pulumi components for Vercel web applications and Blob storage.
package vercel

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	vercelprov "github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*SharedResource)(nil)

// Shared declares cross-environment Vercel Project, CustomEnvironments, ProtectionBypass, Domains, and optional BlobStore.
type Shared struct {
	Name                  string
	Project               string
	Repo                  string
	Environment           string
	VercelProjectName     string
	TeamID                string
	TeamSlug              string
	Framework             string
	RootDirectory         string
	OIDCIssuerMode        string
	DeploymentProtection  string
	BaseDomain            string
	DevEnvironmentSlug    string
	EnableDevEnvironment  bool
	StageEnvironmentSlug  string
	SkipStageEnvironment  bool
	DevDomain             string
	StageDomain           string
	ProdDomain            string
	ExtraDevDomains       []string
	ExtraStageDomains     []string
	ExtraProdDomains      []string
	EnableBlobStore       bool
	BlobAccess            string
	BlobRegion            string
	ProtectionBypassToken string
}

// SharedResource is the provisioned handle for the shared Vercel Project, custom environments, domains, and bypass secret.
type SharedResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	Project                  *vercelprov.Project
	DevEnvironment           *vercelprov.CustomEnvironment
	StageEnvironment         *vercelprov.CustomEnvironment
	ProtectionBypass         *vercelprov.ProjectProtectionBypass
	DevProjectDomain         *vercelprov.ProjectDomain
	ProdProjectDomain        *vercelprov.ProjectDomain
	StageProjectDomain       *vercelprov.ProjectDomain
	ExtraProjectDomains      []*vercelprov.ProjectDomain
	BlobStore                *vercelprov.BlobStore
	BlobConnection           *vercelprov.BlobProjectConnection
	ProjectID                pulumi.StringOutput
	VercelProjectName        pulumi.StringOutput
	TeamID                   pulumi.StringOutput
	TeamSlug                 pulumi.StringOutput
	BaseDomain               pulumi.StringOutput
	DevCustomEnvironmentID   pulumi.StringOutput
	StageCustomEnvironmentID pulumi.StringOutput
	ProtectionBypassSecret   pulumi.StringOutput
	DevDomainName            pulumi.StringOutput
	ProdDomainName           pulumi.StringOutput
	StageDomainName          pulumi.StringOutput
	BlobStoreID              pulumi.StringOutput
	BlobBaseURL              pulumi.StringOutput
}

// Register provisions cross-environment Vercel project prerequisites in the "shared" stack.
func (s Shared) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*SharedResource, error) {
	s.Name = defaultVercelStr(s.Name, "shared")
	env := defaultVercelStr(s.Environment, string(core.EnvShared))
	res := &SharedResource{}
	meta, err := core.InitStateless(ctx, "openbase:vercel:Shared", "vercel-shared", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        s.Name,
		Project:     s.Project,
		Repo:        s.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionSharedVercel(ctx, s.Name, meta, s, res); err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env: map[string]pulumi.StringOutput{
			"VERCEL_PROJECT_ID": res.ProjectID,
		},
		Secrets: map[string]pulumi.StringOutput{
			"VERCEL_AUTOMATION_BYPASS_SECRET": res.ProtectionBypassSecret,
		},
	})
	if err := registerSharedVercelOutputs(ctx, res); err != nil {
		return nil, err
	}
	return res, nil
}

// DeriveBlobBaseURL computes https://<slug>.public.blob.vercel-storage.com from a Vercel store_... ID.
func DeriveBlobBaseURL(storeID pulumi.StringInput) pulumi.StringOutput {
	return storeID.ToStringOutput().ApplyT(func(id string) string {
		slug := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "store_"))
		if slug == "" {
			return ""
		}
		return fmt.Sprintf("https://%s.public.blob.vercel-storage.com", slug)
	}).(pulumi.StringOutput)
}

// Private helpers ------------------------------------------------------------

func provisionSharedVercel(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Shared,
	res *SharedResource,
) error {
	proj, vProjName, err := createVercelProject(ctx, name, meta, spec, res)
	if err != nil {
		return err
	}
	res.Project = proj
	res.ProjectID = proj.ID().ToStringOutput()
	res.VercelProjectName = pulumi.String(vProjName).ToStringOutput()
	res.TeamID = proj.TeamId
	res.TeamSlug = pulumi.String(spec.TeamSlug).ToStringOutput()
	res.BaseDomain = pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput()

	if err := createDevCustomEnv(ctx, name, spec, proj, res); err != nil {
		return err
	}
	if err := createStageCustomEnv(ctx, name, spec, proj, res); err != nil {
		return err
	}
	if err := createSharedDomains(ctx, name, meta.Project, vProjName, spec, proj, res); err != nil {
		return err
	}
	if err := createAutomationBypass(ctx, name, spec, proj, res); err != nil {
		return err
	}
	return createOptionalSharedBlob(ctx, name, meta, spec, proj, res)
}

func createVercelProject(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Shared,
	parent pulumi.Resource,
) (*vercelprov.Project, string, error) {
	vProjName := defaultVercelStr(spec.VercelProjectName, core.BoundedName("openbase-"+meta.Project, 52))
	issuerMode := defaultVercelStr(spec.OIDCIssuerMode, "team")
	protMode := defaultVercelStr(spec.DeploymentProtection, "standard_protection_new")
	projArgs := &vercelprov.ProjectArgs{
		Name:          pulumi.String(vProjName),
		TeamId:        optionalVercelStr(spec.TeamID),
		Framework:     optionalVercelStr(spec.Framework),
		RootDirectory: optionalVercelStr(spec.RootDirectory),
		IgnoreCommand: pulumi.String("exit 0"),
		AutomaticallyExposeSystemEnvironmentVariables: pulumi.Bool(true),
		OidcTokenConfig: &vercelprov.ProjectOidcTokenConfigArgs{
			IssuerMode: pulumi.String(issuerMode),
		},
		VercelAuthentication: &vercelprov.ProjectVercelAuthenticationArgs{
			DeploymentType: pulumi.String(protMode),
		},
	}
	proj, err := vercelprov.NewProject(ctx, name+"-project", projArgs, pulumi.Parent(parent))
	return proj, vProjName, err
}

func createDevCustomEnv(
	ctx *pulumi.Context,
	name string,
	spec Shared,
	proj *vercelprov.Project,
	res *SharedResource,
) error {
	if !spec.EnableDevEnvironment {
		res.DevCustomEnvironmentID = pulumi.String("").ToStringOutput()
		return nil
	}
	slug := defaultVercelStr(spec.DevEnvironmentSlug, "dev")
	ce, err := vercelprov.NewCustomEnvironment(ctx, name+"-dev-env", &vercelprov.CustomEnvironmentArgs{
		ProjectId:   proj.ID(),
		TeamId:      optionalVercelStr(spec.TeamID),
		Name:        pulumi.String(slug),
		Description: pulumi.String("OpenBase development environment"),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.DevEnvironment = ce
	res.DevCustomEnvironmentID = ce.ID().ToStringOutput()
	return nil
}

func createStageCustomEnv(
	ctx *pulumi.Context,
	name string,
	spec Shared,
	proj *vercelprov.Project,
	res *SharedResource,
) error {
	if spec.SkipStageEnvironment {
		res.StageCustomEnvironmentID = pulumi.String("").ToStringOutput()
		return nil
	}
	slug := defaultVercelStr(spec.StageEnvironmentSlug, "staging")
	ce, err := vercelprov.NewCustomEnvironment(ctx, name+"-stage-env", &vercelprov.CustomEnvironmentArgs{
		ProjectId:   proj.ID(),
		TeamId:      optionalVercelStr(spec.TeamID),
		Name:        pulumi.String(slug),
		Description: pulumi.String("OpenBase staging environment"),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.StageEnvironment = ce
	res.StageCustomEnvironmentID = ce.ID().ToStringOutput()
	return nil
}

func createSharedDomains(
	ctx *pulumi.Context,
	name, project, vProjName string,
	spec Shared,
	proj *vercelprov.Project,
	res *SharedResource,
) error {
	prodDom := resolveSharedTierDomain(project, vProjName, core.EnvProd, spec.ProdDomain, spec.BaseDomain, spec.TeamSlug)
	stageDom := resolveSharedTierDomain(project, vProjName, core.EnvStage, spec.StageDomain, spec.BaseDomain, spec.TeamSlug)
	devDom := resolveSharedTierDomain(project, vProjName, core.EnvDev, spec.DevDomain, spec.BaseDomain, spec.TeamSlug)
	res.ProdDomainName = pulumi.String(prodDom).ToStringOutput()
	res.StageDomainName = pulumi.String(stageDom).ToStringOutput()
	res.DevDomainName = pulumi.String(devDom).ToStringOutput()

	pd, err := attachTierDomains(ctx, name+"-prod-domain", prodDom, spec.ExtraProdDomains, nil, spec.TeamID, proj, res)
	if err != nil {
		return err
	}
	res.ProdProjectDomain = pd
	if res.StageEnvironment != nil {
		sd, err := attachTierDomains(ctx, name+"-stage-domain", stageDom, spec.ExtraStageDomains, res.StageEnvironment.ID(), spec.TeamID, proj, res)
		if err != nil {
			return err
		}
		res.StageProjectDomain = sd
	}
	if res.DevEnvironment != nil {
		dd, err := attachTierDomains(ctx, name+"-dev-domain", devDom, spec.ExtraDevDomains, res.DevEnvironment.ID(), spec.TeamID, proj, res)
		if err != nil {
			return err
		}
		res.DevProjectDomain = dd
	}
	return nil
}

func resolveSharedTierDomain(project, vProjName string, env core.Environment, explicit, baseDomain, teamSlug string) string {
	if explicit != "" {
		return core.NormalizeDomain(explicit)
	}
	if baseDomain != "" {
		if dom, err := core.EnvironmentDomain(project, env, "web", baseDomain); err == nil && dom != "" {
			return dom
		}
	}
	if teamSlug == "" {
		return ""
	}
	if env == core.EnvProd {
		return fmt.Sprintf("%s-%s.vercel.app", vProjName, teamSlug)
	}
	return fmt.Sprintf("%s-%s-%s.vercel.app", vProjName, env, teamSlug)
}

func attachTierDomains(
	ctx *pulumi.Context,
	baseName, primary string,
	extras []string,
	customEnvID pulumi.StringPtrInput,
	teamID string,
	proj *vercelprov.Project,
	res *SharedResource,
) (*vercelprov.ProjectDomain, error) {
	all := core.DedupeDomains([]string{primary}, extras)
	var first *vercelprov.ProjectDomain
	for idx, dom := range all {
		resName := baseName
		if idx > 0 {
			resName = fmt.Sprintf("%s-extra-%d", baseName, idx)
		}
		pd, err := vercelprov.NewProjectDomain(ctx, resName, &vercelprov.ProjectDomainArgs{
			ProjectId:           proj.ID(),
			TeamId:              optionalVercelStr(teamID),
			Domain:              pulumi.String(dom),
			CustomEnvironmentId: customEnvID,
			WaitForReady:        pulumi.Bool(strings.HasSuffix(dom, ".vercel.app")),
		}, pulumi.Parent(res))
		if err != nil {
			return nil, err
		}
		if idx == 0 {
			first = pd
		} else {
			res.ExtraProjectDomains = append(res.ExtraProjectDomains, pd)
		}
	}
	return first, nil
}

func createAutomationBypass(
	ctx *pulumi.Context,
	name string,
	spec Shared,
	proj *vercelprov.Project,
	res *SharedResource,
) error {
	bpArgs := &vercelprov.ProjectProtectionBypassArgs{
		ProjectId: proj.ID(),
		TeamId:    optionalVercelStr(spec.TeamID),
		Note:      pulumi.String("OpenBase automation bypass"),
		Secret:    optionalVercelStr(spec.ProtectionBypassToken),
	}
	bp, err := vercelprov.NewProjectProtectionBypass(ctx, name+"-bypass", bpArgs, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.ProtectionBypass = bp
	res.ProtectionBypassSecret = pulumi.ToSecret(bp.Secret).(pulumi.StringOutput)
	return nil
}

func createOptionalSharedBlob(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Shared,
	proj *vercelprov.Project,
	res *SharedResource,
) error {
	if !spec.EnableBlobStore {
		res.BlobStoreID = pulumi.String("").ToStringOutput()
		res.BlobBaseURL = pulumi.String("").ToStringOutput()
		return nil
	}
	store, err := vercelprov.NewBlobStore(ctx, name+"-blob", &vercelprov.BlobStoreArgs{
		Name:   pulumi.String(core.BoundedName(meta.Project+"-blob", 52)),
		Access: pulumi.String(defaultVercelStr(spec.BlobAccess, "public")),
		Region: pulumi.String(defaultVercelStr(spec.BlobRegion, "iad1")),
		TeamId: optionalVercelStr(spec.TeamID),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	conn, err := vercelprov.NewBlobProjectConnection(ctx, name+"-blob-conn", &vercelprov.BlobProjectConnectionArgs{
		BlobStoreId:  store.ID(),
		ProjectId:    proj.ID(),
		TeamId:       optionalVercelStr(spec.TeamID),
		Environments: pulumi.ToStringArray([]string{"development", "preview", "production"}),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.BlobStore = store
	res.BlobConnection = conn
	res.BlobStoreID = store.ID().ToStringOutput()
	res.BlobBaseURL = DeriveBlobBaseURL(store.ID())
	return nil
}

func registerSharedVercelOutputs(ctx *pulumi.Context, res *SharedResource) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"projectId":                res.ProjectID,
		"vercelProjectName":        res.VercelProjectName,
		"teamId":                   res.TeamID,
		"teamSlug":                 res.TeamSlug,
		"baseDomain":               res.BaseDomain,
		"devCustomEnvironmentId":   res.DevCustomEnvironmentID,
		"stageCustomEnvironmentId": res.StageCustomEnvironmentID,
		"protectionBypassSecret":   res.ProtectionBypassSecret,
		"devDomainName":            res.DevDomainName,
		"prodDomainName":           res.ProdDomainName,
		"stageDomainName":          res.StageDomainName,
		"blobStoreId":              res.BlobStoreID,
		"blobBaseUrl":              res.BlobBaseURL,
	})
}

func defaultVercelStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

func optionalVercelStr(val string) pulumi.StringPtrInput {
	if val == "" {
		return nil
	}
	return pulumi.String(val)
}
