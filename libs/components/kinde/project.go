package kinde

import (
	"slices"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	kindeprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-kinde-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*Project)(nil)

// ProjectArgs configures an application's nonProd and prod Kinde OIDC Applications in Premise's infra/shared stack.
type ProjectArgs struct {
	Project                    string
	Repo                       string
	Platform                   *Platform
	PlatformStack              *core.ScopeRef
	PlatformStackRef           string
	AppName                    string
	NonProd                    TenantCredentials
	Prod                       TenantCredentials
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
		Project:     args.Project,
		Repo:        args.Repo,
		Environment: string(core.EnvShared),
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionTwoTenantApps(ctx, name, meta, args, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, buildKindeProjectLink(meta, res))
	return res, registerProjectOidcOutputs(ctx, res)
}

// Private helpers ------------------------------------------------------------

func validateKindeProject(spec *ProjectArgs) error {
	if spec.Platform != nil || spec.PlatformStack != nil || spec.PlatformStackRef != "" {
		return nil
	}
	if !hasTenantCreds(spec.NonProd) || !hasTenantCreds(spec.Prod) {
		return ErrTenantCredentialsRequired
	}
	return nil
}

func provisionTwoTenantApps(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec *ProjectArgs,
	res *Project,
) error {
	nonProdProv, prodProv, err := resolveProjectTenantProviders(ctx, name, spec, res)
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

func resolveProjectTenantProviders(
	ctx *pulumi.Context,
	name string,
	spec *ProjectArgs,
	res *Project,
) (*kindeprov.Provider, *kindeprov.Provider, error) {
	if spec.Platform != nil {
		res.NonProdIssuerURL = spec.Platform.NonProdIssuerURL
		res.ProdIssuerURL = spec.Platform.ProdIssuerURL
		res.EmailPasswordConn = spec.Platform.EmailPasswordConn
		return spec.Platform.NonProdProvider, spec.Platform.ProdProvider, nil
	}
	if hasTenantCreds(spec.NonProd) && hasTenantCreds(spec.Prod) {
		return createExplicitTenantProviders(ctx, name, spec, res)
	}
	return createStackRefTenantProviders(ctx, name, spec.PlatformStack, spec.PlatformStackRef, res)
}

func createExplicitTenantProviders(
	ctx *pulumi.Context,
	name string,
	spec *ProjectArgs,
	res *Project,
) (*kindeprov.Provider, *kindeprov.Provider, error) {
	nonProdIssuer := NormalizeIssuerURL(spec.NonProd.Domain)
	prodIssuer := NormalizeIssuerURL(spec.Prod.Domain)
	nonProdProv, err := createTenantProvider(ctx, name+"-nonprod-prov", nonProdIssuer, spec.NonProd, res)
	if err != nil {
		return nil, nil, err
	}
	prodProv, err := createTenantProvider(ctx, name+"-prod-prov", prodIssuer, spec.Prod, res)
	if err != nil {
		return nil, nil, err
	}
	res.NonProdIssuerURL = pulumi.String(nonProdIssuer).ToStringOutput()
	res.ProdIssuerURL = pulumi.String(prodIssuer).ToStringOutput()
	return nonProdProv, prodProv, nil
}

func createStackRefTenantProviders(
	ctx *pulumi.Context,
	name string,
	scopeRef *core.ScopeRef,
	stackRef string,
	res *Project,
) (*kindeprov.Provider, *kindeprov.Provider, error) {
	sr, err := core.ResolveStackReference(ctx, name+"-platform-ref", scopeRef, stackRef)
	if err != nil {
		return nil, nil, err
	}
	nonProdIssuer := sr.GetStringOutput(pulumi.String("nonProdIssuerUrl"))
	prodIssuer := sr.GetStringOutput(pulumi.String("prodIssuerUrl"))
	nonProdProv, err := kindeprov.NewProvider(ctx, name+"-nonprod-prov", &kindeprov.ProviderArgs{
		Domain:       nonProdIssuer,
		Audience:     pulumi.Sprintf("%s/api", nonProdIssuer),
		ClientId:     sr.GetStringOutput(pulumi.String("kindeNonProdM2mClientId")),
		ClientSecret: pulumi.ToSecret(sr.GetStringOutput(pulumi.String("kindeNonProdM2mClientSecret"))).(pulumi.StringOutput),
	}, pulumi.Parent(res))
	if err != nil {
		return nil, nil, err
	}
	prodProv, err := kindeprov.NewProvider(ctx, name+"-prod-prov", &kindeprov.ProviderArgs{
		Domain:       prodIssuer,
		Audience:     pulumi.Sprintf("%s/api", prodIssuer),
		ClientId:     sr.GetStringOutput(pulumi.String("kindeProdM2mClientId")),
		ClientSecret: pulumi.ToSecret(sr.GetStringOutput(pulumi.String("kindeProdM2mClientSecret"))).(pulumi.StringOutput),
	}, pulumi.Parent(res))
	if err != nil {
		return nil, nil, err
	}
	res.NonProdIssuerURL = nonProdIssuer
	res.ProdIssuerURL = prodIssuer
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
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"nonProdIssuerUrl": res.NonProdIssuerURL,
			"nonProdAppId":     res.NonProdAppID,
			"nonProdClientId":  res.NonProdClientID,
			"prodIssuerUrl":    res.ProdIssuerURL,
			"prodAppId":        res.ProdAppID,
			"prodClientId":     res.ProdClientID,
		},
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
