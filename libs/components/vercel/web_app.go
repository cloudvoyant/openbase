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

var _ core.StatelessResource = (*WebApp)(nil)

// ErrProjectSourceRequired reports a missing Vercel project reference on WebAppArgs.
var ErrProjectSourceRequired = errors.New("vercel: shared, sharedStackRef, or vercelProjectId is required")

// WebAppArgs configures a per-environment Vercel WebApp deployment, alias, env vars, and GCP OIDC federation.
type WebAppArgs struct {
	core.StatelessResourceArgs
	Shared                   *Shared
	SharedStackRef           string
	VercelProjectID          pulumi.StringInput
	VercelProjectName        string
	TeamID                   string
	TeamSlug                 string
	StageCustomEnvironmentID pulumi.StringInput
	SourceDir                string
	PathPrefix               string
	Framework                string
	RootDirectory            string
	ProdDomain               string
	StageDomain              string
	PreviewAlias             string
	GCPProject               pulumi.StringPtrInput
	GCPWorkloadIdentityPool  pulumi.StringInput
	GCPVercelOIDCProvider    pulumi.StringInput
}

// WebApp provisions scoped Vercel environment variables, optional GCP WIF identity, Deployment, and Alias.
type WebApp struct {
	pulumi.ResourceState
	core.StatelessMeta

	FederatedGCPAccount *serviceaccount.Account
	EnvVariables        []*vercelprov.ProjectEnvironmentVariable
	Deployment          *vercelprov.Deployment
	Alias               *vercelprov.Alias
	URL                 pulumi.StringOutput
	AliasHost           pulumi.StringOutput
	DeploymentID        pulumi.StringOutput
	FederatedSAEmail    pulumi.StringOutput
}

// NewWebApp registers a Vercel WebApp component satisfying core.StatelessResource.
func NewWebApp(ctx *pulumi.Context, name string, args WebAppArgs, opts ...pulumi.ResourceOption) (*WebApp, error) {
	if args.Shared == nil && args.SharedStackRef == "" && args.VercelProjectID == nil {
		return nil, ErrProjectSourceRequired
	}
	res := &WebApp{}
	meta, err := core.InitStateless(ctx, "openbase:vercel:WebApp", name, "vercel-webapp", res, &res.StatelessMeta, args.StatelessResourceArgs, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionWebApp(ctx, name, meta, args, res); err != nil {
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
	stageEnvID  pulumi.StringOutput
	wifPool     pulumi.StringOutput
	wifProvider pulumi.StringOutput
}

func provisionWebApp(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args WebAppArgs,
	res *WebApp,
) error {
	refs, err := resolveWebAppRefs(ctx, name, meta, args)
	if err != nil {
		return err
	}
	saEmail, err := configureOptionalGCPFederation(ctx, name, meta, args, refs, res)
	if err != nil {
		return err
	}
	res.FederatedSAEmail = saEmail

	resolved, err := resolveWebAppLinks(ctx, meta, args, refs, saEmail)
	if err != nil {
		return err
	}
	if err := createScopedProjectEnvVars(ctx, name, meta, args, refs, resolved, res); err != nil {
		return err
	}
	aliasHost := computeAliasHost(meta, args, refs)
	res.AliasHost = aliasHost
	res.URL = pulumi.Sprintf("https://%s", aliasHost)
	return createOptionalDeploymentAndAlias(ctx, name, meta, args, refs, resolved, aliasHost, res)
}

func resolveWebAppRefs(ctx *pulumi.Context, name string, meta core.ResourceMeta, args WebAppArgs) (webAppRefs, error) {
	refs := initExplicitWebRefs(meta, args)
	applySharedStructWebRefs(&refs, args.Shared)
	if args.SharedStackRef == "" {
		return refs, nil
	}
	sr, err := pulumi.NewStackReference(ctx, name+"-shared-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(args.SharedStackRef)})
	if err != nil {
		return webAppRefs{}, err
	}
	applySharedStackRefWebRefs(&refs, sr, args)
	return refs, nil
}

func applySharedStackRefWebRefs(refs *webAppRefs, sr *pulumi.StackReference, args WebAppArgs) {
	if !isOutSet(refs.projectID) {
		refs.projectID = sr.GetStringOutput(pulumi.String("projectId"))
	}
	if !isOutSet(refs.stageEnvID) {
		refs.stageEnvID = sr.GetStringOutput(pulumi.String("stageCustomEnvironmentId"))
	}
	if args.Shared == nil && args.VercelProjectName == "" {
		refs.projectName = sr.GetStringOutput(pulumi.String("vercelProjectName"))
	}
	if args.Shared == nil && args.TeamSlug == "" {
		refs.teamSlug = sr.GetStringOutput(pulumi.String("teamSlug"))
	}
}

func initExplicitWebRefs(meta core.ResourceMeta, args WebAppArgs) webAppRefs {
	var refs webAppRefs
	if args.VercelProjectID != nil {
		refs.projectID = args.VercelProjectID.ToStringOutput()
	}
	vName := defaultVercelStr(args.VercelProjectName, "openbase-"+meta.Project)
	refs.projectName = pulumi.String(vName).ToStringOutput()
	refs.teamSlug = pulumi.String(args.TeamSlug).ToStringOutput()
	if args.StageCustomEnvironmentID != nil {
		refs.stageEnvID = args.StageCustomEnvironmentID.ToStringOutput()
	}
	if args.GCPWorkloadIdentityPool != nil {
		refs.wifPool = args.GCPWorkloadIdentityPool.ToStringOutput()
	}
	if args.GCPVercelOIDCProvider != nil {
		refs.wifProvider = args.GCPVercelOIDCProvider.ToStringOutput()
	}
	return refs
}

func applySharedStructWebRefs(refs *webAppRefs, shared *Shared) {
	if shared == nil {
		return
	}
	if !isOutSet(refs.projectID) {
		refs.projectID = shared.ProjectID
	}
	refs.projectName = shared.VercelProjectName
	if !isOutSet(refs.stageEnvID) {
		refs.stageEnvID = shared.StageCustomEnvironmentID
	}
	refs.teamSlug = shared.TeamSlug
}

func configureOptionalGCPFederation(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args WebAppArgs,
	refs webAppRefs,
	res *WebApp,
) (pulumi.StringOutput, error) {
	if args.GCPProject == nil || !isOutSet(refs.wifPool) {
		return pulumi.StringOutput{}, nil
	}
	saID, err := core.ServiceAccountID(meta.Project, meta.Environment, name+"-v")
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	sa, err := serviceaccount.NewAccount(ctx, name+"-gcp-wif-sa", &serviceaccount.AccountArgs{
		Project:     args.GCPProject,
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
	args WebAppArgs,
	refs webAppRefs,
	saEmail pulumi.StringOutput,
) (core.ResolvedLinks, error) {
	resolved, err := core.ResolveLinks(core.LinkGrantContext{
		Ctx:                         ctx,
		ConsumerName:                meta.CanonicalName,
		Environment:                 meta.Environment,
		GCPProject:                  args.GCPProject,
		GCPServiceAccountEmail:      saEmail,
		GCPWorkloadIdentityPoolName: refs.wifPool,
		VercelProjectID:             refs.projectID,
		VercelTeamSlug:              refs.teamSlug,
	}, args.Links)
	if err != nil {
		return core.ResolvedLinks{}, err
	}
	if isOutSet(saEmail) {
		resolved.Env["GCP_SERVICE_ACCOUNT_EMAIL"] = saEmail
	}
	if isOutSet(refs.wifProvider) {
		resolved.Env["GCP_WORKLOAD_IDENTITY_PROVIDER"] = pulumi.Sprintf("//iam.googleapis.com/%s", refs.wifProvider)
	}
	for k, v := range args.Env {
		resolved.Env[k] = v.ToStringOutput()
	}
	for k, v := range args.Secrets {
		resolved.Secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return resolved, nil
}

func createScopedProjectEnvVars(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args WebAppArgs,
	refs webAppRefs,
	resolved core.ResolvedLinks,
	res *WebApp,
) error {
	if meta.Environment.IsPreview() {
		return nil
	}
	for _, k := range sortedMapKeys(resolved.Env) {
		if err := createSingleProjectEnvVar(ctx, name, k, resolved.Env[k], false, meta, args, refs, res); err != nil {
			return err
		}
	}
	for _, k := range sortedMapKeys(resolved.Secrets) {
		if err := createSingleProjectEnvVar(ctx, name, k, resolved.Secrets[k], true, meta, args, refs, res); err != nil {
			return err
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
	args WebAppArgs,
	refs webAppRefs,
	res *WebApp,
) error {
	sensitive := isSecret
	if meta.Environment == core.EnvDev {
		sensitive = false
	}
	evArgs := &vercelprov.ProjectEnvironmentVariableArgs{
		ProjectId: refs.projectID,
		TeamId:    optionalVercelStr(args.TeamID),
		Key:       pulumi.String(key),
		Value:     val,
		Sensitive: pulumi.Bool(sensitive),
	}
	applyEnvTarget(evArgs, meta.Environment, refs.stageEnvID)
	slug := core.BoundedName(strings.ToLower(key), 28)
	ev, err := vercelprov.NewProjectEnvironmentVariable(ctx, fmt.Sprintf("%s-env-%s-%s", name, meta.Environment, slug), evArgs, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.EnvVariables = append(res.EnvVariables, ev)
	return nil
}

func applyEnvTarget(evArgs *vercelprov.ProjectEnvironmentVariableArgs, env core.Environment, stageEnvID pulumi.StringOutput) {
	switch env {
	case core.EnvDev:
		evArgs.Targets = pulumi.ToStringArray([]string{"development"})
	case core.EnvStage:
		if isOutSet(stageEnvID) {
			evArgs.CustomEnvironmentIds = pulumi.StringArray{stageEnvID}
		} else {
			evArgs.Targets = pulumi.ToStringArray([]string{"preview"})
		}
	default:
		evArgs.Targets = pulumi.ToStringArray([]string{"production"})
	}
}

func computeAliasHost(meta core.ResourceMeta, args WebAppArgs, refs webAppRefs) pulumi.StringOutput {
	if explicit := explicitAliasOverride(meta.Environment, args); explicit != "" {
		return pulumi.String(stripScheme(explicit)).ToStringOutput()
	}
	return pulumi.All(refs.projectName, refs.teamSlug).ApplyT(func(vs []any) string {
		return defaultAliasForEnv(meta.Environment, vs[0].(string), defaultVercelStr(vs[1].(string), "local"))
	}).(pulumi.StringOutput)
}

func explicitAliasOverride(env core.Environment, args WebAppArgs) string {
	switch {
	case env.IsPreview():
		return args.PreviewAlias
	case env == core.EnvStage:
		return args.StageDomain
	case env == core.EnvProd:
		return args.ProdDomain
	default:
		return ""
	}
}

func defaultAliasForEnv(env core.Environment, proj, team string) string {
	switch {
	case env.IsPreview():
		return fmt.Sprintf("%s-%s-%s.vercel.app", proj, env.PreviewID(), team)
	case env == core.EnvStage:
		return fmt.Sprintf("%s-stage-%s.vercel.app", proj, team)
	case env == core.EnvDev:
		return fmt.Sprintf("%s-dev-%s.vercel.app", proj, team)
	default:
		return fmt.Sprintf("%s-%s.vercel.app", proj, team)
	}
}

func createOptionalDeploymentAndAlias(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args WebAppArgs,
	refs webAppRefs,
	resolved core.ResolvedLinks,
	aliasHost pulumi.StringOutput,
	res *WebApp,
) error {
	if args.SourceDir == "" {
		res.DeploymentID = pulumi.String("").ToStringOutput()
		return nil
	}
	dirInfo, err := vercelprov.GetProjectDirectory(ctx, &vercelprov.GetProjectDirectoryArgs{Path: args.SourceDir})
	if err != nil {
		return err
	}
	depArgs := buildDeploymentArgs(meta, args, refs, resolved, dirInfo.Files)
	dep, err := vercelprov.NewDeployment(ctx, name+"-deploy", depArgs, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Deployment = dep
	res.DeploymentID = dep.ID().ToStringOutput()
	alias, err := vercelprov.NewAlias(ctx, name+"-alias", &vercelprov.AliasArgs{
		DeploymentId: dep.ID(),
		Alias:        aliasHost,
		TeamId:       optionalVercelStr(args.TeamID),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Alias = alias
	return nil
}

func buildDeploymentArgs(
	meta core.ResourceMeta,
	args WebAppArgs,
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
		TeamId:          optionalVercelStr(args.TeamID),
		Files:           pulumi.ToStringMap(files),
		PathPrefix:      optionalVercelStr(args.PathPrefix),
		Production:      pulumi.Bool(meta.Environment == core.EnvProd),
		DeleteOnDestroy: pulumi.Bool(!meta.Environment.IsProtected()),
		Environment:     mergedEnv,
	}
	if meta.Environment == core.EnvStage && isOutSet(refs.stageEnvID) {
		depArgs.CustomEnvironmentId = refs.stageEnvID
	}
	return depArgs
}

func registerWebAppOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *WebApp) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName":    pulumi.String(meta.CanonicalName),
		"url":              res.URL,
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

func stripScheme(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(raw), "https://"), "http://")
	return strings.TrimRight(s, "/")
}
