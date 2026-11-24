package kinde

import (
	"slices"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	kindeprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-kinde-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatelessResource = (*Project)(nil)
	_ ProjectInput           = (*Project)(nil)
	_ ProjectInput           = (*ProjectRef)(nil)
)

// ProjectInput is satisfied by in-memory *Project handles and cross-stack *ProjectRef lookups.
type ProjectInput interface {
	kindeProjectValues() kindeProjectData
}

// ProjectArgs configures an application's nonProd and prod Kinde OIDC Applications in Premise's infra/shared stack.
type ProjectArgs struct {
	ProjectName                string
	Repo                       string
	Account                    AccountInput
	NonProdEnvironment         string
	ProdEnvironment            string
	AppName                    string
	NonProd                    EnvironmentCredentials
	Prod                       EnvironmentCredentials
	VercelProjectName          string
	VercelTeamSlug             string
	BaseDomain                 string
	DevDomain                  string
	StageDomain                string
	ProdDomain                 string
	ExtraDevDomains            []string
	ExtraStageDomains          []string
	ExtraProdDomains           []string
	CallbackPath               string
	ExtraNonProdRedirectURIs   []string
	ExtraNonProdLogoutURIs     []string
	ExtraProdRedirectURIs      []string
	ExtraProdLogoutURIs        []string
	ImportNonProdAppID         string
	ImportProdAppID            string
	RetainApplicationsOnDelete bool
	SkipEmailPasswordConn      bool
}

// Project is the provisioned handle for an application's nonProd and prod Kinde OIDC Applications.
type Project struct {
	pulumi.ResourceState
	core.StatelessMeta

	NonProdProvider     *kindeprov.Provider
	ProdProvider        *kindeprov.Provider
	NonProdApplication  *kindeprov.Application
	ProdApplication     *kindeprov.Application
	NonProdEmailConn    *kindeprov.ApplicationConnection
	NonProdIssuerURL    pulumi.StringOutput
	NonProdAppID        pulumi.StringOutput
	NonProdClientID     pulumi.StringOutput
	NonProdClientSecret pulumi.StringOutput
	ProdIssuerURL       pulumi.StringOutput
	ProdAppID           pulumi.StringOutput
	ProdClientID        pulumi.StringOutput
	ProdClientSecret    pulumi.StringOutput
	EmailPasswordConn   string
	vercelProjectName   string
	vercelTeamSlug      string
	baseDomain          string
}

// ProjectRef provides typed cross-stack access to a shared kinde.Project exported in infra/shared.
type ProjectRef struct {
	NonProdIssuerURL    pulumi.StringOutput
	NonProdAppID        pulumi.StringOutput
	NonProdClientID     pulumi.StringOutput
	NonProdClientSecret pulumi.StringOutput
	ProdIssuerURL       pulumi.StringOutput
	ProdAppID           pulumi.StringOutput
	ProdClientID        pulumi.StringOutput
	ProdClientSecret    pulumi.StringOutput
	VercelProjectName   pulumi.StringOutput
	VercelTeamSlug      pulumi.StringOutput
	BaseDomain          pulumi.StringOutput
}

// NewProject provisions nonProd and prod Kinde OIDC Applications on the shared stack.
func NewProject(ctx *pulumi.Context, name string, args *ProjectArgs, opts ...pulumi.ResourceOption) (*Project, error) {
	if err := core.RequireSharedStack(ctx); err != nil {
		return nil, err
	}
	if args == nil {
		args = &ProjectArgs{}
	}
	if err := validateKindeProject(args); err != nil {
		return nil, err
	}
	name = defaultKindeStr(name, "kinde")
	res := &Project{}
	meta, err := core.InitStateless(ctx, "openbase:kinde:Project", "kinde-project", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        name,
		Project:     args.ProjectName,
		Repo:        args.Repo,
		Environment: string(core.EnvShared),
	}, opts...)
	if err != nil {
		return nil, err
	}
	res.vercelProjectName = defaultVercelProjectForKinde(meta.Project, args)
	res.vercelTeamSlug = args.VercelTeamSlug
	res.baseDomain = core.NormalizeDomain(args.BaseDomain)
	if err := provisionTwoEnvApps(ctx, name, meta, args, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, buildKindeProjectLink(meta, res))
	return res, registerProjectOidcOutputs(ctx, res)
}

// LookupProject resolves a shared kinde.Project by alias name from an infra/shared ScopeRef.
func LookupProject(shared *core.ScopeRef, name string) *ProjectRef {
	if shared == nil {
		return nil
	}
	alias := []string{name}
	return &ProjectRef{
		NonProdIssuerURL:    shared.ManifestAttr("kinde-project", alias, "nonProdIssuerUrl"),
		NonProdAppID:        shared.ManifestAttr("kinde-project", alias, "nonProdAppId"),
		NonProdClientID:     shared.ManifestAttr("kinde-project", alias, "nonProdClientId"),
		NonProdClientSecret: pulumi.ToSecret(shared.ManifestAttr("kinde-project", alias, "nonProdClientSecret")).(pulumi.StringOutput),
		ProdIssuerURL:       shared.ManifestAttr("kinde-project", alias, "prodIssuerUrl"),
		ProdAppID:           shared.ManifestAttr("kinde-project", alias, "prodAppId"),
		ProdClientID:        shared.ManifestAttr("kinde-project", alias, "prodClientId"),
		ProdClientSecret:    pulumi.ToSecret(shared.ManifestAttr("kinde-project", alias, "prodClientSecret")).(pulumi.StringOutput),
		VercelProjectName:   shared.ManifestAttr("kinde-project", alias, "vercelProjectName", "vercelProjectName"),
		VercelTeamSlug:      shared.ManifestAttr("kinde-project", alias, "teamSlug", "teamSlug"),
		BaseDomain:          shared.ManifestAttr("kinde-project", alias, "baseDomain", "baseDomain"),
	}
}

// Private helpers ------------------------------------------------------------

type kindeProjectData struct {
	nonProdIssuerURL    pulumi.StringOutput
	nonProdClientID     pulumi.StringOutput
	nonProdClientSecret pulumi.StringOutput
	prodIssuerURL       pulumi.StringOutput
	prodClientID        pulumi.StringOutput
	prodClientSecret    pulumi.StringOutput
	vercelProjectName   pulumi.StringOutput
	vercelTeamSlug      pulumi.StringOutput
	baseDomain          pulumi.StringOutput
}

func (p *Project) kindeProjectValues() kindeProjectData {
	if p == nil {
		return kindeProjectData{}
	}
	return kindeProjectData{
		nonProdIssuerURL:    p.NonProdIssuerURL,
		nonProdClientID:     p.NonProdClientID,
		nonProdClientSecret: p.NonProdClientSecret,
		prodIssuerURL:       p.ProdIssuerURL,
		prodClientID:        p.ProdClientID,
		prodClientSecret:    p.ProdClientSecret,
		vercelProjectName:   pulumi.String(p.vercelProjectName).ToStringOutput(),
		vercelTeamSlug:      pulumi.String(p.vercelTeamSlug).ToStringOutput(),
		baseDomain:          pulumi.String(p.baseDomain).ToStringOutput(),
	}
}

func (r *ProjectRef) kindeProjectValues() kindeProjectData {
	if r == nil {
		return kindeProjectData{}
	}
	return kindeProjectData{
		nonProdIssuerURL:    r.NonProdIssuerURL,
		nonProdClientID:     r.NonProdClientID,
		nonProdClientSecret: r.NonProdClientSecret,
		prodIssuerURL:       r.ProdIssuerURL,
		prodClientID:        r.ProdClientID,
		prodClientSecret:    r.ProdClientSecret,
		vercelProjectName:   r.VercelProjectName,
		vercelTeamSlug:      r.VercelTeamSlug,
		baseDomain:          r.BaseDomain,
	}
}

func validateKindeProject(spec *ProjectArgs) error {
	if spec.Account != nil {
		return nil
	}
	if !hasEnvCreds(spec.NonProd) || !hasEnvCreds(spec.Prod) {
		return ErrEnvironmentCredentialsRequired
	}
	return nil
}

func provisionTwoEnvApps(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec *ProjectArgs,
	res *Project,
) error {
	nonProdProv, prodProv, err := resolveProjectEnvProviders(ctx, name, spec, res)
	if err != nil {
		return err
	}
	res.NonProdProvider = nonProdProv
	res.ProdProvider = prodProv
	appName := defaultKindeStr(spec.AppName, "openbase-"+meta.Project)
	if err := createNonProdApplication(ctx, name, appName, meta.Project, spec, nonProdProv, res); err != nil {
		return err
	}
	return createProdApplication(ctx, name, appName, meta, spec, prodProv, res)
}

func resolveProjectEnvProviders(
	ctx *pulumi.Context,
	name string,
	spec *ProjectArgs,
	res *Project,
) (*kindeprov.Provider, *kindeprov.Provider, error) {
	if hasEnvCreds(spec.NonProd) && hasEnvCreds(spec.Prod) {
		return createExplicitEnvProviders(ctx, name, spec, res)
	}
	nonProdKey := defaultKindeStr(spec.NonProdEnvironment, DefaultNonProdEnvironment)
	prodKey := defaultKindeStr(spec.ProdEnvironment, DefaultProdEnvironment)
	acc := spec.Account.kindeAccountValues()
	if acc.local != nil {
		return resolveLocalAccountProviders(acc.local, nonProdKey, prodKey, res)
	}
	return createAccountRefProviders(ctx, name, acc.ref, nonProdKey, prodKey, res)
}

func resolveLocalAccountProviders(
	local *Account,
	nonProdKey, prodKey string,
	res *Project,
) (*kindeprov.Provider, *kindeprov.Provider, error) {
	nonProdProv, okNP := local.Providers[nonProdKey]
	prodProv, okP := local.Providers[prodKey]
	if !okNP || !okP {
		return nil, nil, ErrEnvironmentCredentialsRequired
	}
	res.NonProdIssuerURL = local.IssuerURLs[nonProdKey]
	res.ProdIssuerURL = local.IssuerURLs[prodKey]
	res.EmailPasswordConn = local.EmailPasswordConn
	return nonProdProv, prodProv, nil
}

func createExplicitEnvProviders(
	ctx *pulumi.Context,
	name string,
	spec *ProjectArgs,
	res *Project,
) (*kindeprov.Provider, *kindeprov.Provider, error) {
	nonProdIssuer := NormalizeIssuerURL(spec.NonProd.Domain)
	prodIssuer := NormalizeIssuerURL(spec.Prod.Domain)
	nonProdProv, err := createEnvProvider(ctx, name+"-nonprod-prov", nonProdIssuer, spec.NonProd, res)
	if err != nil {
		return nil, nil, err
	}
	prodProv, err := createEnvProvider(ctx, name+"-prod-prov", prodIssuer, spec.Prod, res)
	if err != nil {
		return nil, nil, err
	}
	res.NonProdIssuerURL = pulumi.String(nonProdIssuer).ToStringOutput()
	res.ProdIssuerURL = pulumi.String(prodIssuer).ToStringOutput()
	return nonProdProv, prodProv, nil
}

func createAccountRefProviders(
	ctx *pulumi.Context,
	name string,
	ref *AccountRef,
	nonProdKey, prodKey string,
	res *Project,
) (*kindeprov.Provider, *kindeprov.Provider, error) {
	npEnv := ref.Environment(nonProdKey)
	pEnv := ref.Environment(prodKey)
	nonProdProv, err := kindeprov.NewProvider(ctx, name+"-nonprod-prov", &kindeprov.ProviderArgs{
		Domain:       npEnv.IssuerURL,
		Audience:     pulumi.Sprintf("%s/api", npEnv.IssuerURL),
		ClientId:     npEnv.M2MClientID,
		ClientSecret: pulumi.ToSecret(npEnv.M2MClientSecret).(pulumi.StringOutput),
	}, pulumi.Parent(res))
	if err != nil {
		return nil, nil, err
	}
	prodProv, err := kindeprov.NewProvider(ctx, name+"-prod-prov", &kindeprov.ProviderArgs{
		Domain:       pEnv.IssuerURL,
		Audience:     pulumi.Sprintf("%s/api", pEnv.IssuerURL),
		ClientId:     pEnv.M2MClientID,
		ClientSecret: pulumi.ToSecret(pEnv.M2MClientSecret).(pulumi.StringOutput),
	}, pulumi.Parent(res))
	if err != nil {
		return nil, nil, err
	}
	res.NonProdIssuerURL = npEnv.IssuerURL
	res.ProdIssuerURL = pEnv.IssuerURL
	return nonProdProv, prodProv, nil
}

func createNonProdApplication(
	ctx *pulumi.Context,
	name, appName, project string,
	spec *ProjectArgs,
	prov *kindeprov.Provider,
	res *Project,
) error {
	redirects, logouts := buildNonProdCallbacks(project, spec)
	appOpts := buildKindeAppOptions(res, prov, spec.ImportNonProdAppID, spec.RetainApplicationsOnDelete)
	app, err := kindeprov.NewApplication(ctx, name+"-nonprod-app", &kindeprov.ApplicationArgs{
		Name:         pulumi.String(appName),
		Type:         pulumi.String("reg"),
		RedirectUris: pulumi.ToStringArray(redirects),
		LogoutUris:   pulumi.ToStringArray(logouts),
	}, appOpts...)
	if err != nil {
		return err
	}
	res.NonProdApplication = app
	res.NonProdAppID = app.ID().ToStringOutput()
	res.NonProdClientID = app.ClientId
	res.NonProdClientSecret = pulumi.ToSecret(app.ClientSecret).(pulumi.StringOutput)
	return attachNonProdEmailPassword(ctx, name, spec, prov, app, res)
}

func attachNonProdEmailPassword(
	ctx *pulumi.Context,
	name string,
	spec *ProjectArgs,
	prov *kindeprov.Provider,
	app *kindeprov.Application,
	res *Project,
) error {
	if spec.SkipEmailPasswordConn {
		return nil
	}
	connID := res.EmailPasswordConn
	if connID == "" {
		conns, err := kindeprov.GetConnections(ctx, nil, pulumi.Provider(prov))
		if err != nil {
			return err
		}
		connID = findEmailPasswordConn(conns.Connections)
	}
	if connID == "" {
		return nil
	}
	res.EmailPasswordConn = connID
	ac, err := kindeprov.NewApplicationConnection(ctx, name+"-nonprod-email-pw", &kindeprov.ApplicationConnectionArgs{
		ApplicationId: app.ID(),
		ConnectionId:  pulumi.String(connID),
	}, pulumi.Parent(res), pulumi.Provider(prov), pulumi.RetainOnDelete(true))
	if err != nil {
		return err
	}
	res.NonProdEmailConn = ac
	return nil
}

func createProdApplication(
	ctx *pulumi.Context,
	name, appName string,
	meta core.ResourceMeta,
	spec *ProjectArgs,
	prov *kindeprov.Provider,
	res *Project,
) error {
	redirects, logouts := buildProdCallbacks(meta.Project, spec)
	appOpts := buildKindeAppOptions(res, prov, spec.ImportProdAppID, spec.RetainApplicationsOnDelete)
	app, err := kindeprov.NewApplication(ctx, name+"-prod-app", &kindeprov.ApplicationArgs{
		Name:         pulumi.String(appName),
		Type:         pulumi.String("reg"),
		RedirectUris: pulumi.ToStringArray(redirects),
		LogoutUris:   pulumi.ToStringArray(logouts),
	}, appOpts...)
	if err != nil {
		return err
	}
	res.ProdApplication = app
	res.ProdAppID = app.ID().ToStringOutput()
	res.ProdClientID = app.ClientId
	res.ProdClientSecret = pulumi.ToSecret(app.ClientSecret).(pulumi.StringOutput)
	return nil
}

func buildKindeAppOptions(
	parent pulumi.Resource,
	prov *kindeprov.Provider,
	importID string,
	retain bool,
) []pulumi.ResourceOption {
	opts := []pulumi.ResourceOption{pulumi.Parent(parent), pulumi.Provider(prov)}
	if retain {
		opts = append(opts, pulumi.RetainOnDelete(true))
	}
	if importID != "" {
		opts = append(opts, pulumi.Import(pulumi.ID(importID)), pulumi.IgnoreChanges([]string{"name"}))
	}
	return opts
}

func defaultVercelProjectForKinde(project string, spec *ProjectArgs) string {
	if spec.VercelProjectName != "" {
		return spec.VercelProjectName
	}
	if spec.VercelTeamSlug != "" && project != "" {
		return core.BoundedName("openbase-"+project, 52)
	}
	return project
}

func buildNonProdCallbacks(project string, spec *ProjectArgs) ([]string, []string) {
	cb := ensureLeadingSlash(defaultKindeStr(spec.CallbackPath, "/api/auth/kinde_callback"))
	redirects := []string{"http://localhost:5173" + cb, "http://localhost:3000" + cb}
	logouts := []string{"http://localhost:5173/", "http://localhost:3000/"}
	vProj := defaultVercelProjectForKinde(project, spec)
	for _, host := range nonProdCallbackHosts(project, vProj, spec) {
		origin := NormalizeIssuerURL(host)
		redirects = append(redirects, origin+cb)
		logouts = append(logouts, origin+"/")
	}
	redirects = append(redirects, spec.ExtraNonProdRedirectURIs...)
	logouts = append(logouts, spec.ExtraNonProdLogoutURIs...)
	return sortAndCompact(redirects), sortAndCompact(logouts)
}

func nonProdCallbackHosts(project, vProj string, spec *ProjectArgs) []string {
	hosts := nonProdVercelHosts(vProj, spec.VercelTeamSlug)
	if base := core.NormalizeDomain(spec.BaseDomain); base != "" && project != "" {
		hosts = append(hosts, "https://"+project+"-*."+base)
	}
	for _, d := range core.DedupeDomains([]string{spec.DevDomain}, spec.ExtraDevDomains) {
		hosts = append(hosts, "https://"+d)
	}
	return hosts
}

func nonProdVercelHosts(vProj, teamSlug string) []string {
	if vProj == "" || teamSlug == "" {
		return nil
	}
	return []string{
		"https://" + vProj + ".vercel.app",
		"https://" + vProj + "-" + teamSlug + ".vercel.app",
		"https://" + vProj + "-*-" + teamSlug + ".vercel.app",
	}
}

func buildProdCallbacks(project string, spec *ProjectArgs) ([]string, []string) {
	cb := ensureLeadingSlash(defaultKindeStr(spec.CallbackPath, "/api/auth/kinde_callback"))
	vProj := defaultVercelProjectForKinde(project, spec)
	var redirects, logouts []string
	for _, host := range prodVercelAndCustomHosts(project, vProj, spec) {
		origin := NormalizeIssuerURL(host)
		redirects = append(redirects, origin+cb)
		logouts = append(logouts, origin+"/")
	}
	redirects = append(redirects, spec.ExtraProdRedirectURIs...)
	logouts = append(logouts, spec.ExtraProdLogoutURIs...)
	return sortAndCompact(redirects), sortAndCompact(logouts)
}

func prodVercelAndCustomHosts(project, vProj string, spec *ProjectArgs) []string {
	var hosts []string
	if base := core.NormalizeDomain(spec.BaseDomain); base != "" && project != "" {
		if pd, err := core.EnvironmentDomain(project, core.EnvProd, "web", base); err == nil {
			hosts = append(hosts, pd)
		}
		if sd, err := core.EnvironmentDomain(project, core.EnvStage, "web", base); err == nil {
			hosts = append(hosts, sd)
		}
	}
	hosts = appendProdFallbackOrExplicit(hosts, vProj, spec)
	return core.DedupeDomains(hosts, spec.ExtraProdDomains, spec.ExtraStageDomains)
}

func appendProdFallbackOrExplicit(hosts []string, vProj string, spec *ProjectArgs) []string {
	if spec.ProdDomain != "" {
		hosts = append(hosts, spec.ProdDomain)
	} else if spec.VercelTeamSlug != "" {
		hosts = append(hosts, vProj+".vercel.app", vProj+"-"+spec.VercelTeamSlug+".vercel.app")
	}
	if spec.StageDomain != "" {
		hosts = append(hosts, spec.StageDomain)
	} else if spec.VercelTeamSlug != "" {
		hosts = append(hosts, vProj+"-stage-"+spec.VercelTeamSlug+".vercel.app")
	}
	return hosts
}

func sortAndCompact(items []string) []string {
	slices.Sort(items)
	return slices.Compact(items)
}

func buildKindeProjectLink(meta core.ResourceMeta, res *Project) core.LinkDescriptor {
	attrs := map[string]pulumi.StringOutput{
		"nonProdIssuerUrl":    res.NonProdIssuerURL,
		"nonProdAppId":        res.NonProdAppID,
		"nonProdClientId":     res.NonProdClientID,
		"nonProdClientSecret": res.NonProdClientSecret,
		"prodIssuerUrl":       res.ProdIssuerURL,
		"prodAppId":           res.ProdAppID,
		"prodClientId":        res.ProdClientID,
		"prodClientSecret":    res.ProdClientSecret,
		"vercelProjectName":   pulumi.String(res.vercelProjectName).ToStringOutput(),
		"teamSlug":            pulumi.String(res.vercelTeamSlug).ToStringOutput(),
		"baseDomain":          pulumi.String(res.baseDomain).ToStringOutput(),
	}
	return core.LinkDescriptor{
		Name:       meta.Name,
		Kind:       meta.Kind,
		Attributes: attrs,
		StackExports: map[string]pulumi.Input{
			"nonProdIssuerUrl":    res.NonProdIssuerURL,
			"nonProdAppId":        res.NonProdAppID,
			"nonProdClientId":     res.NonProdClientID,
			"nonProdClientSecret": res.NonProdClientSecret,
			"prodIssuerUrl":       res.ProdIssuerURL,
			"prodAppId":           res.ProdAppID,
			"prodClientId":        res.ProdClientID,
			"prodClientSecret":    res.ProdClientSecret,
		},
	}
}

func registerProjectOidcOutputs(ctx *pulumi.Context, res *Project) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"nonProdIssuerUrl":    res.NonProdIssuerURL,
		"nonProdAppId":        res.NonProdAppID,
		"nonProdClientId":     res.NonProdClientID,
		"nonProdClientSecret": res.NonProdClientSecret,
		"prodIssuerUrl":       res.ProdIssuerURL,
		"prodAppId":           res.ProdAppID,
		"prodClientId":        res.ProdClientID,
		"prodClientSecret":    res.ProdClientSecret,
		"emailPasswordConn":   pulumi.String(res.EmailPasswordConn),
	})
}
