package vercel

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	vercelprov "github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatelessResource              = (*ProjectResource)(nil)
	_ core.ScopedSpecOf[*ProjectResource] = Project{}
)

// Project declares an application's Vercel Project, CustomEnvironments, ProtectionBypass, Domains, and shared BlobStore in Premise's infra/shared stack.
type Project struct {
	Name                  string
	Project               string
	Repo                  string
	Platform              *PlatformResource
	PlatformStackRef      string
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
	PreviewEnv            map[string]pulumi.StringInput
	PreviewSecrets        map[string]pulumi.StringInput
}

// ProjectResource is the provisioned handle for an application's Vercel Project, custom environments, domains, and bypass secret.
type ProjectResource struct {
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
	PreviewEnvVariables      []*vercelprov.ProjectEnvironmentVariable
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

// Register provisions cross-environment Vercel project assets in Premise's infra/shared stack.
func (p Project) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*ProjectResource, error) {
	p.Name = defaultVercelStr(p.Name, "vercel")
	res := &ProjectResource{}
	meta, err := core.InitStateless(ctx, "openbase:vercel:Project", "vercel-project", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        p.Name,
		Project:     p.Project,
		Repo:        p.Repo,
		Environment: string(core.EnvShared),
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionVercelProject(ctx, p.Name, meta, p, res); err != nil {
		return nil, err
	}
	res.SetLink(buildVercelProjectLink(meta, res))
	return res, registerVercelProjectOutputs(ctx, res)
}

// RegisterInScope adapts vercel.Project to openbase.Project.
func (p Project) RegisterInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (core.Resource, error) {
	return p.RegisterTypedInScope(ctx, scope, opts...)
}

// RegisterTypedInScope registers vercel.Project with inherited Project and PlatformRef defaults.
func (p Project) RegisterTypedInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (*ProjectResource, error) {
	if p.Project == "" {
		p.Project = scope.Project
	}
	if p.Repo == "" {
		p.Repo = scope.Repo
	}
	if p.Platform == nil && scope.Platform != nil {
		if plat, ok := scope.Platform.PlatformHandle("vercel-platform").(*PlatformResource); ok {
			p.Platform = plat
		}
	}
	if p.PlatformStackRef == "" {
		p.PlatformStackRef = scope.PlatformStackName()
	}
	return p.Register(ctx, opts...)
}

// Private helpers ------------------------------------------------------------

type resolvedVercelPlatform struct {
	teamID   pulumi.StringOutput
	teamSlug pulumi.StringOutput
	baseDom  pulumi.StringOutput
}

func provisionVercelProject(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Project,
	res *ProjectResource,
) error {
	plat, err := resolveVercelPlatformRefs(ctx, name, spec)
	if err != nil {
		return err
	}
	proj, vProjName, err := createUnderlyingVercelProject(ctx, name, meta, spec, plat.teamID, res)
	if err != nil {
		return err
	}
	res.Project = proj
	res.ProjectID = proj.ID().ToStringOutput()
	res.VercelProjectName = pulumi.String(vProjName).ToStringOutput()
	res.TeamID = proj.TeamId
	res.TeamSlug = plat.teamSlug
	res.BaseDomain = plat.baseDom

	if err := provisionVercelProjectChildren(ctx, name, meta, vProjName, spec, plat, proj, res); err != nil {
		return err
	}
	return seedOptionalPreviewEnvVars(ctx, name, spec, res)
}

func provisionVercelProjectChildren(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	vProjName string,
	spec Project,
	plat resolvedVercelPlatform,
	proj *vercelprov.Project,
	res *ProjectResource,
) error {
	if err := createDevCustomEnv(ctx, name, spec, plat.teamID, proj, res); err != nil {
		return err
	}
	if err := createStageCustomEnv(ctx, name, spec, plat.teamID, proj, res); err != nil {
		return err
	}
	if err := createProjectDomains(ctx, name, meta.Project, vProjName, spec, plat, proj, res); err != nil {
		return err
	}
	if err := createAutomationBypass(ctx, name, spec, plat.teamID, proj, res); err != nil {
		return err
	}
	return createOptionalProjectBlob(ctx, name, meta, spec, plat.teamID, proj, res)
}

func seedOptionalPreviewEnvVars(
	ctx *pulumi.Context,
	name string,
	spec Project,
	res *ProjectResource,
) error {
	if len(spec.PreviewEnv) == 0 && len(spec.PreviewSecrets) == 0 {
		return nil
	}
	plain := make(map[string]pulumi.StringOutput, len(spec.PreviewEnv))
	for k, v := range spec.PreviewEnv {
		plain[k] = v.ToStringOutput()
	}
	secrets := make(map[string]pulumi.StringOutput, len(spec.PreviewSecrets))
	for k, v := range spec.PreviewSecrets {
		secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	if err := writePreviewEnvMap(ctx, name, plain, false, res); err != nil {
		return err
	}
	return writePreviewEnvMap(ctx, name, secrets, true, res)
}

func writePreviewEnvMap(
	ctx *pulumi.Context,
	name string,
	vars map[string]pulumi.StringOutput,
	sensitive bool,
	res *ProjectResource,
) error {
	teamPtr := nonEmptyStringPtrOutput(res.TeamID)
	for _, k := range sortedMapKeys(vars) {
		slug := core.BoundedName(strings.ToLower(k), 28)
		ev, err := vercelprov.NewProjectEnvironmentVariable(ctx, fmt.Sprintf("%s-preview-%s", name, slug), &vercelprov.ProjectEnvironmentVariableArgs{
			ProjectId: res.ProjectID,
			TeamId:    teamPtr,
			Targets:   pulumi.ToStringArray([]string{"preview"}),
			Key:       pulumi.String(k),
			Value:     vars[k],
			Sensitive: pulumi.Bool(sensitive),
		}, pulumi.Parent(res))
		if err != nil {
			return err
		}
		res.PreviewEnvVariables = append(res.PreviewEnvVariables, ev)
	}
	return nil
}

func resolveVercelPlatformRefs(ctx *pulumi.Context, name string, spec Project) (resolvedVercelPlatform, error) {
	p := resolvedVercelPlatform{
		teamID:   pulumi.String(spec.TeamID).ToStringOutput(),
		teamSlug: pulumi.String(spec.TeamSlug).ToStringOutput(),
		baseDom:  pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput(),
	}
	applyVercelPlatformStruct(&p, spec)
	if spec.PlatformStackRef == "" {
		return p, nil
	}
	sr, err := pulumi.NewStackReference(ctx, name+"-platform-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(spec.PlatformStackRef)})
	if err != nil {
		return resolvedVercelPlatform{}, err
	}
	applyVercelPlatformStackRef(&p, sr, spec)
	return p, nil
}

func applyVercelPlatformStruct(p *resolvedVercelPlatform, spec Project) {
	if spec.Platform == nil {
		return
	}
	if spec.TeamID == "" {
		p.teamID = spec.Platform.TeamID
	}
	if spec.TeamSlug == "" {
		p.teamSlug = spec.Platform.TeamSlug
	}
	if spec.BaseDomain == "" {
		p.baseDom = spec.Platform.BaseDomain
	}
}

func applyVercelPlatformStackRef(p *resolvedVercelPlatform, sr *pulumi.StackReference, spec Project) {
	if spec.Platform != nil {
		return
	}
	if spec.TeamID == "" {
		p.teamID = optionalVercelStackOutput(sr, "teamId")
	}
	if spec.TeamSlug == "" {
		p.teamSlug = optionalVercelStackOutput(sr, "teamSlug")
	}
	if spec.BaseDomain == "" {
		p.baseDom = preferVercelStackOutput(sr, "vercelBaseDomain", "baseDomain")
	}
}

func createUnderlyingVercelProject(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Project,
	teamID pulumi.StringOutput,
	parent pulumi.Resource,
) (*vercelprov.Project, string, error) {
	vProjName := defaultVercelStr(spec.VercelProjectName, core.BoundedName("openbase-"+meta.Project, 52))
	issuerMode := defaultVercelStr(spec.OIDCIssuerMode, "team")
	protMode := defaultVercelStr(spec.DeploymentProtection, "standard_protection_new")
	projArgs := &vercelprov.ProjectArgs{
		Name:          pulumi.String(vProjName),
		TeamId:        nonEmptyStringPtrOutput(teamID),
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
	spec Project,
	teamID pulumi.StringOutput,
	proj *vercelprov.Project,
	res *ProjectResource,
) error {
	if !spec.EnableDevEnvironment {
		res.DevCustomEnvironmentID = pulumi.String("").ToStringOutput()
		return nil
	}
	slug := defaultVercelStr(spec.DevEnvironmentSlug, "dev")
	ce, err := vercelprov.NewCustomEnvironment(ctx, name+"-dev-env", &vercelprov.CustomEnvironmentArgs{
		ProjectId:   proj.ID(),
		TeamId:      nonEmptyStringPtrOutput(teamID),
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
	spec Project,
	teamID pulumi.StringOutput,
	proj *vercelprov.Project,
	res *ProjectResource,
) error {
	if spec.SkipStageEnvironment {
		res.StageCustomEnvironmentID = pulumi.String("").ToStringOutput()
		return nil
	}
	slug := defaultVercelStr(spec.StageEnvironmentSlug, "staging")
	ce, err := vercelprov.NewCustomEnvironment(ctx, name+"-stage-env", &vercelprov.CustomEnvironmentArgs{
		ProjectId:   proj.ID(),
		TeamId:      nonEmptyStringPtrOutput(teamID),
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

func createProjectDomains(
	ctx *pulumi.Context,
	name, project, vProjName string,
	spec Project,
	plat resolvedVercelPlatform,
	proj *vercelprov.Project,
	res *ProjectResource,
) error {
	res.ProdDomainName = computeProjectTierDomainOutput(project, vProjName, core.EnvProd, spec.ProdDomain, plat)
	res.StageDomainName = computeProjectTierDomainOutput(project, vProjName, core.EnvStage, spec.StageDomain, plat)
	res.DevDomainName = computeProjectTierDomainOutput(project, vProjName, core.EnvDev, spec.DevDomain, plat)

	if shouldAttachPrimaryProjectDomain(spec.ProdDomain, spec) {
		pd, err := attachTierDomains(ctx, name+"-prod-domain", res.ProdDomainName, spec.ProdDomain, spec.ExtraProdDomains, nil, plat.teamID, proj, res)
		if err != nil {
			return err
		}
		res.ProdProjectDomain = pd
	}
	if res.StageEnvironment != nil && shouldAttachPrimaryProjectDomain(spec.StageDomain, spec) {
		sd, err := attachTierDomains(ctx, name+"-stage-domain", res.StageDomainName, spec.StageDomain, spec.ExtraStageDomains, res.StageEnvironment.ID(), plat.teamID, proj, res)
		if err != nil {
			return err
		}
		res.StageProjectDomain = sd
	}
	if res.DevEnvironment != nil && shouldAttachPrimaryProjectDomain(spec.DevDomain, spec) {
		dd, err := attachTierDomains(ctx, name+"-dev-domain", res.DevDomainName, spec.DevDomain, spec.ExtraDevDomains, res.DevEnvironment.ID(), plat.teamID, proj, res)
		if err != nil {
			return err
		}
		res.DevProjectDomain = dd
	}
	return nil
}

func shouldAttachPrimaryProjectDomain(explicit string, spec Project) bool {
	return explicit != "" || spec.BaseDomain != "" || spec.TeamSlug != "" || spec.Platform != nil || spec.PlatformStackRef != ""
}

func computeProjectTierDomainOutput(
	project, vProjName string,
	env core.Environment,
	explicit string,
	plat resolvedVercelPlatform,
) pulumi.StringOutput {
	if explicit != "" {
		return pulumi.String(core.NormalizeDomain(explicit)).ToStringOutput()
	}
	return pulumi.All(plat.baseDom, plat.teamSlug).ApplyT(func(vs []any) string {
		base, team := vs[0].(string), vs[1].(string)
		return resolveTierDomainValue(project, vProjName, env, base, team)
	}).(pulumi.StringOutput)
}

func resolveTierDomainValue(project, vProjName string, env core.Environment, baseDomain, teamSlug string) string {
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
	baseName string,
	primaryOut pulumi.StringOutput,
	explicitPrimary string,
	extras []string,
	customEnvID pulumi.StringPtrInput,
	teamID pulumi.StringOutput,
	proj *vercelprov.Project,
	res *ProjectResource,
) (*vercelprov.ProjectDomain, error) {
	waitPrimary := explicitPrimary == "" && strings.HasSuffix(explicitPrimary, ".vercel.app")
	first, err := vercelprov.NewProjectDomain(ctx, baseName, &vercelprov.ProjectDomainArgs{
		ProjectId:           proj.ID(),
		TeamId:              nonEmptyStringPtrOutput(teamID),
		Domain:              primaryOut,
		CustomEnvironmentId: customEnvID,
		WaitForReady:        pulumi.Bool(waitPrimary),
	}, pulumi.Parent(res))
	if err != nil {
		return nil, err
	}
	for idx, dom := range core.DedupeDomains(extras) {
		pd, err := vercelprov.NewProjectDomain(ctx, fmt.Sprintf("%s-extra-%d", baseName, idx+1), &vercelprov.ProjectDomainArgs{
			ProjectId:           proj.ID(),
			TeamId:              nonEmptyStringPtrOutput(teamID),
			Domain:              pulumi.String(dom),
			CustomEnvironmentId: customEnvID,
			WaitForReady:        pulumi.Bool(strings.HasSuffix(dom, ".vercel.app")),
		}, pulumi.Parent(res))
		if err != nil {
			return nil, err
		}
		res.ExtraProjectDomains = append(res.ExtraProjectDomains, pd)
	}
	return first, nil
}

func createAutomationBypass(
	ctx *pulumi.Context,
	name string,
	spec Project,
	teamID pulumi.StringOutput,
	proj *vercelprov.Project,
	res *ProjectResource,
) error {
	bp, err := vercelprov.NewProjectProtectionBypass(ctx, name+"-bypass", &vercelprov.ProjectProtectionBypassArgs{
		ProjectId: proj.ID(),
		TeamId:    nonEmptyStringPtrOutput(teamID),
		Note:      pulumi.String("OpenBase automation bypass"),
		Secret:    optionalVercelStr(spec.ProtectionBypassToken),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.ProtectionBypass = bp
	res.ProtectionBypassSecret = pulumi.ToSecret(bp.Secret).(pulumi.StringOutput)
	return nil
}

func createOptionalProjectBlob(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Project,
	teamID pulumi.StringOutput,
	proj *vercelprov.Project,
	res *ProjectResource,
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
		TeamId: nonEmptyStringPtrOutput(teamID),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	conn, err := vercelprov.NewBlobProjectConnection(ctx, name+"-blob-conn", &vercelprov.BlobProjectConnectionArgs{
		BlobStoreId:  store.ID(),
		ProjectId:    proj.ID(),
		TeamId:       nonEmptyStringPtrOutput(teamID),
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

func buildVercelProjectLink(meta core.ResourceMeta, res *ProjectResource) core.LinkDescriptor {
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"projectId":                res.ProjectID,
			"vercelProjectName":        res.VercelProjectName,
			"teamId":                   res.TeamID,
			"teamSlug":                 res.TeamSlug,
			"baseDomain":               res.BaseDomain,
			"devCustomEnvironmentId":   res.DevCustomEnvironmentID,
			"stageCustomEnvironmentId": res.StageCustomEnvironmentID,
			"devDomainName":            res.DevDomainName,
			"prodDomainName":           res.ProdDomainName,
			"stageDomainName":          res.StageDomainName,
			"blobStoreId":              res.BlobStoreID,
			"blobBaseUrl":              res.BlobBaseURL,
		},
		StackExports: map[string]pulumi.Input{
			"vercelProjectId":          res.ProjectID,
			"vercelProjectName":        res.VercelProjectName,
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
		},
	}
}

func registerVercelProjectOutputs(ctx *pulumi.Context, res *ProjectResource) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"projectId":                res.ProjectID,
		"vercelProjectId":          res.ProjectID,
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
