package vercel

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/serviceaccount"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	vercelprov "github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*WebAppResource)(nil)

// ErrProjectSourceRequired reports a missing Vercel project reference on WebApp.
var ErrProjectSourceRequired = errors.New("vercel: vercelProject, projectStackRef, or vercelProjectId is required")

// WebApp declares a per-environment Vercel WebApp deployment, aliases, env vars, and GCP OIDC federation.
type WebApp struct {
	Name                     string
	Project                  string
	Repo                     string
	Environment              string
	Kind                     string
	Links                    []core.Resource
	Env                      map[string]pulumi.StringInput
	Secrets                  map[string]pulumi.StringInput
	VercelProject            *ProjectResource
	ProjectStackRef          string
	VercelProjectID          pulumi.StringInput
	VercelProjectName        string
	TeamID                   string
	TeamSlug                 string
	BaseDomain               string
	EnableDevEnvironment     bool
	SkipStageEnvironment     bool
	DevCustomEnvironmentID   pulumi.StringInput
	StageCustomEnvironmentID pulumi.StringInput
	SourceDir                string
	PathPrefix               string
	Framework                string
	RootDirectory            string
	DevDomain                string
	ProdDomain               string
	StageDomain              string
	PreviewAlias             string
	ExtraDomains             []string
	GCPProject               pulumi.StringPtrInput
	GCPWorkloadIdentityPool  pulumi.StringInput
	GCPVercelOIDCProvider    pulumi.StringInput
}

// WebAppResource is the provisioned Vercel WebApp handle with scoped env vars, GCP WIF identity, Deployment, and Aliases.
type WebAppResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	FederatedGCPAccount *serviceaccount.Account
	EnvVariables        []*vercelprov.ProjectEnvironmentVariable
	Deployment          *vercelprov.Deployment
	Alias               *vercelprov.Alias
	Aliases             []*vercelprov.Alias
	CustomDomains       []*vercelprov.ProjectDomain
	URL                 pulumi.StringOutput
	URLs                pulumi.StringArrayOutput
	AliasHost           pulumi.StringOutput
	DeploymentID        pulumi.StringOutput
	FederatedSAEmail    pulumi.StringOutput
}

// Register provisions a Vercel WebApp component satisfying core.StatelessResource.
func (w WebApp) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*WebAppResource, error) {
	if w.VercelProject == nil && w.ProjectStackRef == "" && w.VercelProjectID == nil {
		return nil, ErrProjectSourceRequired
	}
	res := &WebAppResource{}
	meta, err := core.InitStateless(ctx, "openbase:vercel:WebApp", "vercel-webapp", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        w.Name,
		Project:     w.Project,
		Repo:        w.Repo,
		Environment: w.Environment,
		Kind:        w.Kind,
		Links:       w.Links,
		Env:         w.Env,
		Secrets:     w.Secrets,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionWebApp(ctx, w.Name, meta, w, res); err != nil {
		return nil, err
	}
	urlKey := strings.ToUpper(strings.ReplaceAll(meta.Name, "-", "_")) + "_URL"
	res.SetLink(core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env:  map[string]pulumi.StringOutput{urlKey: res.URL},
	})
	if err := registerWebAppOutputs(ctx, meta, res); err != nil {
		return nil, err
	}
	return res, nil
}

// Private helpers ------------------------------------------------------------

type webAppRefs struct {
	projectID   pulumi.StringOutput
	projectName pulumi.StringOutput
	teamSlug    pulumi.StringOutput
	baseDomain  pulumi.StringOutput
	devEnvID    pulumi.StringOutput
	stageEnvID  pulumi.StringOutput
	wifPool     pulumi.StringOutput
	wifProvider pulumi.StringOutput
}

func provisionWebApp(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec WebApp,
	res *WebAppResource,
) error {
	refs, err := resolveWebAppRefs(ctx, name, meta, spec)
	if err != nil {
		return err
	}
	saEmail, err := configureOptionalGCPFederation(ctx, name, meta, spec, refs, res)
	if err != nil {
		return err
	}
	res.FederatedSAEmail = saEmail

	resolved, err := resolveWebAppLinks(ctx, meta, spec, refs, saEmail)
	if err != nil {
		return err
	}
	if err := createScopedProjectEnvVars(ctx, name, meta, spec, refs, resolved, res); err != nil {
		return err
	}
	aliasHost := computeAliasHost(meta, spec, refs)
	res.AliasHost = aliasHost
	res.URL = pulumi.Sprintf("https://%s", aliasHost)
	res.URLs = computeAllWebAppURLs(aliasHost, spec.ExtraDomains)
	return createOptionalDeploymentAndAliases(ctx, name, meta, spec, refs, resolved, aliasHost, res)
}

func resolveWebAppRefs(ctx *pulumi.Context, name string, meta core.ResourceMeta, spec WebApp) (webAppRefs, error) {
	refs := initExplicitWebRefs(meta, spec)
	applyProjectStructWebRefs(&refs, spec.VercelProject)
	if spec.ProjectStackRef == "" {
		return refs, nil
	}
	sr, err := pulumi.NewStackReference(ctx, name+"-project-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(spec.ProjectStackRef)})
	if err != nil {
		return webAppRefs{}, err
	}
	applyProjectStackRefWebRefs(&refs, sr, spec)
	return refs, nil
}

func applyProjectStackRefWebRefs(refs *webAppRefs, sr *pulumi.StackReference, spec WebApp) {
	if !isOutSet(refs.projectID) {
		refs.projectID = preferVercelStackOutput(sr, "vercelProjectId", "projectId")
	}
	if !isOutSet(refs.devEnvID) {
		refs.devEnvID = optionalVercelStackOutput(sr, "devCustomEnvironmentId")
	}
	if !isOutSet(refs.stageEnvID) {
		refs.stageEnvID = optionalVercelStackOutput(sr, "stageCustomEnvironmentId")
	}
	applyOptionalProjectNamingRefs(refs, sr, spec)
}

func applyOptionalProjectNamingRefs(refs *webAppRefs, sr *pulumi.StackReference, spec WebApp) {
	if spec.VercelProject != nil {
		return
	}
	if spec.VercelProjectName == "" {
		refs.projectName = optionalVercelStackOutput(sr, "vercelProjectName")
	}
	if spec.TeamSlug == "" {
		refs.teamSlug = optionalVercelStackOutput(sr, "teamSlug")
	}
	if spec.BaseDomain == "" {
		refs.baseDomain = optionalVercelStackOutput(sr, "baseDomain")
	}
}

func preferVercelStackOutput(sr *pulumi.StackReference, primaryKey, fallbackKey string) pulumi.StringOutput {
	return pulumi.All(
		sr.GetOutput(pulumi.String(primaryKey)),
		sr.GetOutput(pulumi.String(fallbackKey)),
	).ApplyT(func(vs []any) string {
		if first, ok := vs[0].(string); ok && first != "" {
			return first
		}
		if second, ok := vs[1].(string); ok {
			return second
		}
		return ""
	}).(pulumi.StringOutput)
}

func optionalVercelStackOutput(sr *pulumi.StackReference, key string) pulumi.StringOutput {
	return sr.GetOutput(pulumi.String(key)).ApplyT(func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}).(pulumi.StringOutput)
}

func initExplicitWebRefs(meta core.ResourceMeta, spec WebApp) webAppRefs {
	var refs webAppRefs
	if spec.VercelProjectID != nil {
		refs.projectID = spec.VercelProjectID.ToStringOutput()
	}
	vName := defaultVercelStr(spec.VercelProjectName, "openbase-"+meta.Project)
	refs.projectName = pulumi.String(vName).ToStringOutput()
	refs.teamSlug = pulumi.String(spec.TeamSlug).ToStringOutput()
	refs.baseDomain = pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput()
	if spec.DevCustomEnvironmentID != nil {
		refs.devEnvID = spec.DevCustomEnvironmentID.ToStringOutput()
	}
	if spec.StageCustomEnvironmentID != nil {
		refs.stageEnvID = spec.StageCustomEnvironmentID.ToStringOutput()
	}
	if spec.GCPWorkloadIdentityPool != nil {
		refs.wifPool = spec.GCPWorkloadIdentityPool.ToStringOutput()
	}
	if spec.GCPVercelOIDCProvider != nil {
		refs.wifProvider = spec.GCPVercelOIDCProvider.ToStringOutput()
	}
	return refs
}

func applyProjectStructWebRefs(refs *webAppRefs, proj *ProjectResource) {
	if proj == nil {
		return
	}
	if !isOutSet(refs.projectID) {
		refs.projectID = proj.ProjectID
	}
	refs.projectName = proj.VercelProjectName
	refs.teamSlug = proj.TeamSlug
	refs.baseDomain = proj.BaseDomain
	if !isOutSet(refs.devEnvID) && proj.DevEnvironment != nil {
		refs.devEnvID = proj.DevCustomEnvironmentID
	}
	if !isOutSet(refs.stageEnvID) && proj.StageEnvironment != nil {
		refs.stageEnvID = proj.StageCustomEnvironmentID
	}
}

func configureOptionalGCPFederation(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec WebApp,
	refs webAppRefs,
	res *WebAppResource,
) (pulumi.StringOutput, error) {
	if spec.GCPProject == nil || !isOutSet(refs.wifPool) {
		return pulumi.StringOutput{}, nil
	}
	saID, err := core.ServiceAccountID(meta.Project, meta.Environment, name+"-v")
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	sa, err := serviceaccount.NewAccount(ctx, name+"-gcp-wif-sa", &serviceaccount.AccountArgs{
		Project:     spec.GCPProject,
		AccountId:   pulumi.String(saID),
		DisplayName: pulumi.String(core.BoundedName(meta.CanonicalName+"-vercel", 63)),
	}, pulumi.Parent(res))
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	member := pulumi.Sprintf("principalSet://iam.googleapis.com/%s/attribute.project_id/%s", refs.wifPool, refs.projectID)
	_, err = serviceaccount.NewIAMMember(ctx, name+"-gcp-wif-user", &serviceaccount.IAMMemberArgs{
		ServiceAccountId: sa.Name,
		Role:             pulumi.String("roles/iam.workloadIdentityUser"),
		Member:           member,
	}, pulumi.Parent(res))
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	res.FederatedGCPAccount = sa
	return sa.Email, nil
}

func resolveWebAppLinks(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec WebApp,
	refs webAppRefs,
	saEmail pulumi.StringOutput,
) (core.ResolvedLinks, error) {
	resolved, err := core.ResolveLinks(core.LinkGrantContext{
		Ctx:                         ctx,
		ConsumerName:                meta.CanonicalName,
		Environment:                 meta.Environment,
		GCPProject:                  spec.GCPProject,
		GCPServiceAccountEmail:      saEmail,
		GCPWorkloadIdentityPoolName: refs.wifPool,
		VercelProjectID:             refs.projectID,
		VercelTeamSlug:              refs.teamSlug,
	}, spec.Links)
	if err != nil {
		return core.ResolvedLinks{}, err
	}
	if isOutSet(saEmail) {
		resolved.Env["GCP_SERVICE_ACCOUNT_EMAIL"] = saEmail
	}
	if isOutSet(refs.wifProvider) {
		resolved.Env["GCP_WORKLOAD_IDENTITY_PROVIDER"] = pulumi.Sprintf("//iam.googleapis.com/%s", refs.wifProvider)
	}
	for k, v := range spec.Env {
		resolved.Env[k] = v.ToStringOutput()
	}
	for k, v := range spec.Secrets {
		resolved.Secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return resolved, nil
}

func createScopedProjectEnvVars(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec WebApp,
	refs webAppRefs,
	resolved core.ResolvedLinks,
	res *WebAppResource,
) error {
	if shouldSkipProjectEnvVars(meta.Environment, spec, refs) {
		return nil
	}
	useDevCustom := usesDevCustomEnv(spec)
	skipKeys := blobManagedProjectEnvKeys(meta.Environment, useDevCustom, spec.Links)
	if err := registerProjectEnvMap(ctx, name, resolved.Env, false, skipKeys, meta, spec, refs, res); err != nil {
		return err
	}
	return registerProjectEnvMap(ctx, name, resolved.Secrets, true, skipKeys, meta, spec, refs, res)
}

func shouldSkipProjectEnvVars(env core.Environment, spec WebApp, refs webAppRefs) bool {
	if env.IsPreview() {
		return true
	}
	if env == core.EnvStage {
		return spec.SkipStageEnvironment || !isOutSet(refs.stageEnvID)
	}
	return false
}

func usesDevCustomEnv(spec WebApp) bool {
	return spec.EnableDevEnvironment || spec.DevCustomEnvironmentID != nil || (spec.VercelProject != nil && spec.VercelProject.DevEnvironment != nil)
}

func registerProjectEnvMap(
	ctx *pulumi.Context,
	name string,
	vars map[string]pulumi.StringOutput,
	isSecret bool,
	skipKeys map[string]bool,
	meta core.ResourceMeta,
	spec WebApp,
	refs webAppRefs,
	res *WebAppResource,
) error {
	for _, k := range sortedMapKeys(vars) {
		if skipKeys[k] {
			continue
		}
		if err := createSingleProjectEnvVar(ctx, name, k, vars[k], isSecret, meta, spec, refs, res); err != nil {
			return err
		}
	}
	return nil
}

func blobManagedProjectEnvKeys(env core.Environment, useDevCustom bool, links []core.Resource) map[string]bool {
	for _, link := range links {
		if link != nil && link.Link().Kind == "vercel-blob" {
			skip := map[string]bool{"BLOB_READ_WRITE_TOKEN": true}
			if env == core.EnvStage || (env == core.EnvDev && useDevCustom) {
				skip["BLOB_BASE_URL"] = true
			}
			return skip
		}
	}
	return nil
}

func createSingleProjectEnvVar(
	ctx *pulumi.Context,
	name, key string,
	val pulumi.StringOutput,
	isSecret bool,
	meta core.ResourceMeta,
	spec WebApp,
	refs webAppRefs,
	res *WebAppResource,
) error {
	useDevCustom := usesDevCustomEnv(spec)
	sensitive := isSecret
	if meta.Environment == core.EnvDev && !useDevCustom {
		sensitive = false
	}
	evArgs := &vercelprov.ProjectEnvironmentVariableArgs{
		ProjectId: refs.projectID,
		TeamId:    optionalVercelStr(spec.TeamID),
		Key:       pulumi.String(key),
		Value:     val,
		Sensitive: pulumi.Bool(sensitive),
	}
	applyEnvTarget(evArgs, meta.Environment, useDevCustom, refs)
	slug := core.BoundedName(strings.ToLower(key), 28)
	ev, err := vercelprov.NewProjectEnvironmentVariable(ctx, fmt.Sprintf("%s-env-%s-%s", name, meta.Environment, slug), evArgs, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.EnvVariables = append(res.EnvVariables, ev)
	return nil
}

func applyEnvTarget(
	evArgs *vercelprov.ProjectEnvironmentVariableArgs,
	env core.Environment,
	useDevCustom bool,
	refs webAppRefs,
) {
	switch {
	case env == core.EnvDev && useDevCustom && isOutSet(refs.devEnvID):
		evArgs.CustomEnvironmentIds = pulumi.StringArray{refs.devEnvID}
	case env == core.EnvDev:
		evArgs.Targets = pulumi.ToStringArray([]string{"development"})
	case env == core.EnvStage:
		evArgs.CustomEnvironmentIds = pulumi.StringArray{refs.stageEnvID}
	default:
		evArgs.Targets = pulumi.ToStringArray([]string{"production"})
	}
}

func computeAliasHost(meta core.ResourceMeta, spec WebApp, refs webAppRefs) pulumi.StringOutput {
	if explicit := explicitAliasOverride(meta.Environment, spec); explicit != "" {
		return pulumi.String(core.NormalizeDomain(explicit)).ToStringOutput()
	}
	return pulumi.All(refs.projectName, refs.teamSlug, refs.baseDomain).ApplyT(func(vs []any) string {
		vProj := defaultVercelStr(vs[0].(string), "openbase-"+meta.Project)
		team := defaultVercelStr(vs[1].(string), "local")
		base := core.NormalizeDomain(vs[2].(string))
		if base != "" {
			if dom, err := core.EnvironmentDomain(meta.Project, meta.Environment, meta.Name, base); err == nil && dom != "" {
				return dom
			}
		}
		return defaultAliasForEnv(meta.Environment, vProj, team)
	}).(pulumi.StringOutput)
}

func explicitAliasOverride(env core.Environment, spec WebApp) string {
	switch {
	case env.IsPreview():
		return spec.PreviewAlias
	case env == core.EnvDev:
		return spec.DevDomain
	case env == core.EnvStage:
		return spec.StageDomain
	case env == core.EnvProd:
		return spec.ProdDomain
	default:
		return ""
	}
}

func defaultAliasForEnv(env core.Environment, proj, team string) string {
	switch {
	case env.IsPreview():
		return fmt.Sprintf("%s-%s-%s.vercel.app", proj, core.EnvDomainLabel(env), team)
	case env == core.EnvStage:
		return fmt.Sprintf("%s-stage-%s.vercel.app", proj, team)
	case env == core.EnvDev:
		return fmt.Sprintf("%s-dev-%s.vercel.app", proj, team)
	default:
		return fmt.Sprintf("%s-%s.vercel.app", proj, team)
	}
}

func computeAllWebAppURLs(primaryHost pulumi.StringOutput, extraDomains []string) pulumi.StringArrayOutput {
	extras := core.DedupeDomains(extraDomains)
	return primaryHost.ApplyT(func(primary string) []string {
		urls := []string{"https://" + primary}
		for _, d := range extras {
			if d != primary {
				urls = append(urls, "https://"+d)
			}
		}
		return urls
	}).(pulumi.StringArrayOutput)
}

func createOptionalDeploymentAndAliases(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec WebApp,
	refs webAppRefs,
	resolved core.ResolvedLinks,
	aliasHost pulumi.StringOutput,
	res *WebAppResource,
) error {
	if spec.SourceDir == "" {
		res.DeploymentID = pulumi.String("").ToStringOutput()
		return nil
	}
	dirInfo, err := vercelprov.GetProjectDirectory(ctx, &vercelprov.GetProjectDirectoryArgs{Path: spec.SourceDir})
	if err != nil {
		return err
	}
	depArgs := buildDeploymentArgs(meta, spec, refs, resolved, dirInfo.Files)
	dep, err := vercelprov.NewDeployment(ctx, name+"-deploy", depArgs, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Deployment = dep
	res.DeploymentID = dep.ID().ToStringOutput()
	return bindWebAppAliases(ctx, name, meta, spec, refs, dep, aliasHost, res)
}

func bindWebAppAliases(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec WebApp,
	refs webAppRefs,
	dep *vercelprov.Deployment,
	aliasHost pulumi.StringOutput,
	res *WebAppResource,
) error {
	primary, err := vercelprov.NewAlias(ctx, name+"-alias", &vercelprov.AliasArgs{
		DeploymentId: dep.ID(),
		Alias:        aliasHost,
		TeamId:       optionalVercelStr(spec.TeamID),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Alias = primary
	res.Aliases = append(res.Aliases, primary)
	return attachExtraDeploymentAliases(ctx, name, meta, spec, refs, dep, res)
}

func attachExtraDeploymentAliases(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec WebApp,
	refs webAppRefs,
	dep *vercelprov.Deployment,
	res *WebAppResource,
) error {
	attachProjDom, customEnvID := extraProjectDomainTarget(meta.Environment, spec, refs)
	for idx, dom := range core.DedupeDomains(spec.ExtraDomains) {
		opts := []pulumi.ResourceOption{pulumi.Parent(res)}
		if attachProjDom && !strings.HasSuffix(dom, ".vercel.app") {
			pd, err := vercelprov.NewProjectDomain(ctx, fmt.Sprintf("%s-extra-domain-%d", name, idx), &vercelprov.ProjectDomainArgs{
				ProjectId:           refs.projectID,
				TeamId:              optionalVercelStr(spec.TeamID),
				Domain:              pulumi.String(dom),
				CustomEnvironmentId: customEnvID,
				WaitForReady:        pulumi.Bool(false),
			}, pulumi.Parent(res))
			if err != nil {
				return err
			}
			res.CustomDomains = append(res.CustomDomains, pd)
			opts = append(opts, pulumi.DependsOn([]pulumi.Resource{pd}))
		}
		al, err := vercelprov.NewAlias(ctx, fmt.Sprintf("%s-extra-alias-%d", name, idx), &vercelprov.AliasArgs{
			DeploymentId: dep.ID(),
			Alias:        pulumi.String(dom),
			TeamId:       optionalVercelStr(spec.TeamID),
		}, opts...)
		if err != nil {
			return err
		}
		res.Aliases = append(res.Aliases, al)
	}
	return nil
}

func extraProjectDomainTarget(env core.Environment, spec WebApp, refs webAppRefs) (bool, pulumi.StringPtrInput) {
	switch {
	case env == core.EnvProd:
		return true, nil
	case env == core.EnvStage && !spec.SkipStageEnvironment && isOutSet(refs.stageEnvID):
		return true, nonEmptyStringPtrOutput(refs.stageEnvID)
	case env == core.EnvDev && usesDevCustomEnv(spec) && isOutSet(refs.devEnvID):
		return true, nonEmptyStringPtrOutput(refs.devEnvID)
	default:
		return false, nil
	}
}

func buildDeploymentArgs(
	meta core.ResourceMeta,
	spec WebApp,
	refs webAppRefs,
	resolved core.ResolvedLinks,
	files map[string]string,
) *vercelprov.DeploymentArgs {
	mergedEnv := pulumi.StringMap{
		"OPENBASE_ENVIRONMENT": pulumi.String(meta.Environment.String()),
	}
	for k, v := range resolved.Env {
		mergedEnv[k] = v
	}
	for k, v := range resolved.Secrets {
		mergedEnv[k] = v
	}
	depArgs := &vercelprov.DeploymentArgs{
		ProjectId:       refs.projectID,
		TeamId:          optionalVercelStr(spec.TeamID),
		Files:           pulumi.ToStringMap(files),
		PathPrefix:      optionalVercelStr(spec.PathPrefix),
		Production:      pulumi.Bool(meta.Environment == core.EnvProd),
		DeleteOnDestroy: pulumi.Bool(!meta.Environment.IsProtected()),
		Environment:     mergedEnv,
	}
	applyDeploymentCustomEnv(depArgs, meta.Environment, spec, refs)
	return depArgs
}

func applyDeploymentCustomEnv(
	depArgs *vercelprov.DeploymentArgs,
	env core.Environment,
	spec WebApp,
	refs webAppRefs,
) {
	if env == core.EnvStage && !spec.SkipStageEnvironment && isOutSet(refs.stageEnvID) {
		depArgs.CustomEnvironmentId = nonEmptyStringPtrOutput(refs.stageEnvID)
	}
	if env == core.EnvDev && usesDevCustomEnv(spec) && isOutSet(refs.devEnvID) {
		depArgs.CustomEnvironmentId = nonEmptyStringPtrOutput(refs.devEnvID)
	}
}

func nonEmptyStringPtrOutput(out pulumi.StringOutput) pulumi.StringPtrOutput {
	return out.ApplyT(func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}).(pulumi.StringPtrOutput)
}

func registerWebAppOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *WebAppResource) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName":    pulumi.String(meta.CanonicalName),
		"url":              res.URL,
		"urls":             res.URLs,
		"aliasHost":        res.AliasHost,
		"deploymentId":     res.DeploymentID,
		"federatedSaEmail": res.FederatedSAEmail,
	})
}

func sortedMapKeys(m map[string]pulumi.StringOutput) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
