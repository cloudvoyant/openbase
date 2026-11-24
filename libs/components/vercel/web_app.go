package vercel

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	vercelprov "github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*WebApp)(nil)

// ErrProjectSourceRequired reports a missing Vercel project reference on WebAppArgs.
var ErrProjectSourceRequired = errors.New("vercel: project or vercelProjectId is required")

// WebAppArgs configures a per-environment Vercel WebApp deployment, aliases, env vars, and OIDC identity brokering.
type WebAppArgs struct {
	ProjectName              string
	Repo                     string
	Environment              string
	Kind                     string
	Links                    []core.Resource
	Brokers                  []core.IdentityBroker
	Env                      map[string]pulumi.StringInput
	Secrets                  map[string]pulumi.StringInput
	Project                  ProjectInput
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
	Revision                 string
}

// WebApp is the provisioned Vercel WebApp handle with scoped env vars, Deployment, and Aliases.
type WebApp struct {
	pulumi.ResourceState
	core.StatelessMeta

	EnvVariables  []*vercelprov.ProjectEnvironmentVariable
	Deployment    *vercelprov.Deployment
	Alias         *vercelprov.Alias
	Aliases       []*vercelprov.Alias
	CustomDomains []*vercelprov.ProjectDomain
	URL           pulumi.StringOutput
	URLs          pulumi.StringArrayOutput
	AliasHost     pulumi.StringOutput
	DeploymentID  pulumi.StringOutput
}

// NewWebApp provisions a Vercel WebApp component satisfying core.StatelessResource.
func NewWebApp(ctx *pulumi.Context, name string, args *WebAppArgs, opts ...pulumi.ResourceOption) (*WebApp, error) {
	spec, err := normalizeWebAppArgs(args)
	if err != nil {
		return nil, err
	}
	res := &WebApp{}
	meta, err := core.InitStateless(ctx, "openbase:vercel:WebApp", "vercel-webapp", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        name,
		Project:     spec.ProjectName,
		Repo:        spec.Repo,
		Environment: spec.Environment,
		Kind:        spec.Kind,
		Links:       spec.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionWebApp(ctx, name, meta, spec, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"url":          res.URL,
			"aliasHost":    res.AliasHost,
			"deploymentId": res.DeploymentID,
		},
		StackExports: map[string]pulumi.Input{
			"url":          res.URL,
			"urls":         res.URLs,
			"aliasHost":    res.AliasHost,
			"deploymentId": res.DeploymentID,
		},
	})
	return res, registerWebAppOutputs(ctx, meta, res)
}

// Private helpers ------------------------------------------------------------

func normalizeWebAppArgs(args *WebAppArgs) (WebAppArgs, error) {
	if args == nil || (args.Project == nil && args.VercelProjectID == nil) {
		return WebAppArgs{}, ErrProjectSourceRequired
	}
	spec := *args
	if spec.SourceDir != "" && spec.SourceDir != "." && spec.PathPrefix == "" {
		spec.PathPrefix = spec.SourceDir
	}
	return spec, nil
}

type webAppRefs struct {
	projectID   pulumi.StringOutput
	projectName pulumi.StringOutput
	teamSlug    pulumi.StringOutput
	baseDomain  pulumi.StringOutput
	devEnvID    pulumi.StringOutput
	stageEnvID  pulumi.StringOutput
}

func provisionWebApp(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec WebAppArgs,
	res *WebApp,
) error {
	refs := resolveWebAppRefs(meta, spec)
	resolved, err := resolveWebAppLinks(ctx, meta, spec, refs)
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

func resolveWebAppRefs(meta core.ResourceMeta, spec WebAppArgs) webAppRefs {
	refs := initExplicitWebRefs(meta, spec)
	applyProjectInputWebRefs(&refs, spec)
	return refs
}

func initExplicitWebRefs(meta core.ResourceMeta, spec WebAppArgs) webAppRefs {
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
	return refs
}

func applyProjectInputWebRefs(refs *webAppRefs, spec WebAppArgs) {
	if spec.Project == nil {
		return
	}
	pv := spec.Project.vercelProjectValues()
	if !isOutSet(refs.projectID) && isOutSet(pv.projectID) {
		refs.projectID = pv.projectID
	}
	if !isOutSet(refs.devEnvID) && isOutSet(pv.devCustomEnvironmentID) {
		refs.devEnvID = pv.devCustomEnvironmentID
	}
	if !isOutSet(refs.stageEnvID) && isOutSet(pv.stageCustomEnvironmentID) {
		refs.stageEnvID = pv.stageCustomEnvironmentID
	}
	applyProjectInputNamingRefs(refs, pv, spec)
}

func applyProjectInputNamingRefs(refs *webAppRefs, pv vercelProjectData, spec WebAppArgs) {
	if spec.VercelProjectName == "" && isOutSet(pv.vercelProjectName) {
		refs.projectName = pv.vercelProjectName
	}
	if spec.TeamSlug == "" && isOutSet(pv.teamSlug) {
		refs.teamSlug = pv.teamSlug
	}
	if spec.BaseDomain == "" && isOutSet(pv.baseDomain) {
		refs.baseDomain = pv.baseDomain
	}
}

type webAppEnvVars struct {
	plain   map[string]pulumi.StringOutput
	secrets map[string]pulumi.StringOutput
}

func resolveWebAppLinks(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec WebAppArgs,
	refs webAppRefs,
) (webAppEnvVars, error) {
	if _, err := core.ResolveLinks(core.LinkGrantContext{
		Ctx:          ctx,
		ConsumerName: meta.CanonicalName,
		ConsumerMeta: meta,
		Environment:  meta.Environment,
		Subject: core.Subject{
			Provider: "vercel",
			NativeID: refs.projectID,
			Claims: map[string]pulumi.StringOutput{
				"project_id": refs.projectID,
				"owner":      refs.teamSlug,
			},
		},
		Brokers: spec.Brokers,
	}, spec.Links); err != nil {
		return webAppEnvVars{}, err
	}
	vars := webAppEnvVars{
		plain:   make(map[string]pulumi.StringOutput, len(spec.Env)+2),
		secrets: make(map[string]pulumi.StringOutput, len(spec.Secrets)),
	}
	vars.plain["OB_ENVIRONMENT"] = pulumi.String(meta.Environment.String()).ToStringOutput()
	if spec.Revision != "" {
		vars.plain["OB_REVISION"] = pulumi.String(spec.Revision).ToStringOutput()
	}
	for k, v := range spec.Env {
		vars.plain[k] = v.ToStringOutput()
	}
	for k, v := range spec.Secrets {
		vars.secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return vars, nil
}

func createScopedProjectEnvVars(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec WebAppArgs,
	refs webAppRefs,
	resolved webAppEnvVars,
	res *WebApp,
) error {
	if shouldSkipProjectEnvVars(meta.Environment, spec, refs) {
		return nil
	}
	useDevCustom := usesDevCustomEnv(spec)
	skipKeys := blobManagedProjectEnvKeys(meta.Environment, useDevCustom, spec.Links)
	if err := registerProjectEnvMap(ctx, name, resolved.plain, false, skipKeys, meta, spec, refs, res); err != nil {
		return err
	}
	return registerProjectEnvMap(ctx, name, resolved.secrets, true, skipKeys, meta, spec, refs, res)
}

func shouldSkipProjectEnvVars(env core.Environment, spec WebAppArgs, refs webAppRefs) bool {
	if env.IsPreview() {
		return true
	}
	if env == core.EnvStage {
		return spec.SkipStageEnvironment || !isOutSet(refs.stageEnvID)
	}
	return false
}

func usesDevCustomEnv(spec WebAppArgs) bool {
	return spec.EnableDevEnvironment || spec.DevCustomEnvironmentID != nil || (spec.Project != nil && spec.Project.vercelProjectValues().hasDevCustomEnv)
}

func registerProjectEnvMap(
	ctx *pulumi.Context,
	name string,
	vars map[string]pulumi.StringOutput,
	isSecret bool,
	skipKeys map[string]bool,
	meta core.ResourceMeta,
	spec WebAppArgs,
	refs webAppRefs,
	res *WebApp,
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
	spec WebAppArgs,
	refs webAppRefs,
	res *WebApp,
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

func computeAliasHost(meta core.ResourceMeta, spec WebAppArgs, refs webAppRefs) pulumi.StringOutput {
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

func explicitAliasOverride(env core.Environment, spec WebAppArgs) string {
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
	spec WebAppArgs,
	refs webAppRefs,
	resolved webAppEnvVars,
	aliasHost pulumi.StringOutput,
	res *WebApp,
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
	spec WebAppArgs,
	refs webAppRefs,
	dep *vercelprov.Deployment,
	aliasHost pulumi.StringOutput,
	res *WebApp,
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
	spec WebAppArgs,
	refs webAppRefs,
	dep *vercelprov.Deployment,
	res *WebApp,
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

func extraProjectDomainTarget(env core.Environment, spec WebAppArgs, refs webAppRefs) (bool, pulumi.StringPtrInput) {
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
	spec WebAppArgs,
	refs webAppRefs,
	resolved webAppEnvVars,
	files map[string]string,
) *vercelprov.DeploymentArgs {
	mergedEnv := make(pulumi.StringMap, len(resolved.plain)+len(resolved.secrets))
	for k, v := range resolved.plain {
		mergedEnv[k] = v
	}
	for k, v := range resolved.secrets {
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
	spec WebAppArgs,
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

func registerWebAppOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *WebApp) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"url":           res.URL,
		"urls":          res.URLs,
		"aliasHost":     res.AliasHost,
		"deploymentId":  res.DeploymentID,
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
