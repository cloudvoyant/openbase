package kinde

import (
	"slices"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	kindeprov "github.com/cloudvoyant/openbase/internal/providers/kinde"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*ProjectResource)(nil)

// Project declares an application's dev and prod Kinde OIDC Applications in Premise's infra/shared stack.
type Project struct {
	Name                       string
	Project                    string
	Repo                       string
	Environment                string
	Platform                   *PlatformResource
	PlatformStackRef           string
	AppName                    string
	Dev                        TenantCredentials
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
	ExtraDevRedirectURIs       []string
	ExtraDevLogoutURIs         []string
	ExtraProdRedirectURIs      []string
	ExtraProdLogoutURIs        []string
	ImportDevAppID             string
	ImportProdAppID            string
	RetainApplicationsOnDelete bool
	SkipEmailPasswordConn      bool
}

// ProjectResource is the provisioned handle for an application's dev and prod Kinde OIDC Applications.
type ProjectResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	DevProvider       *kindeprov.Provider
	ProdProvider      *kindeprov.Provider
	DevApplication    *kindeprov.Application
	ProdApplication   *kindeprov.Application
	DevEmailConn      *kindeprov.ApplicationConnection
	DevIssuerURL      pulumi.StringOutput
	DevAppID          pulumi.StringOutput
	DevClientID       pulumi.StringOutput
	DevClientSecret   pulumi.StringOutput
	ProdIssuerURL     pulumi.StringOutput
	ProdAppID         pulumi.StringOutput
	ProdClientID      pulumi.StringOutput
	ProdClientSecret  pulumi.StringOutput
	EmailPasswordConn string
}

// Register provisions dev and prod Kinde OIDC Applications and exports credentials for per-environment kinde.OidcApp stacks.
func (p Project) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*ProjectResource, error) {
	if err := validateKindeProject(p); err != nil {
		return nil, err
	}
	p.Name = defaultKindeStr(p.Name, "kinde")
	env := defaultKindeStr(p.Environment, string(core.EnvShared))
	res := &ProjectResource{}
	meta, err := core.InitStateless(ctx, "openbase:kinde:Project", "kinde-project", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        p.Name,
		Project:     p.Project,
		Repo:        p.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionTwoTenantApps(ctx, p.Name, meta, p, res); err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env: map[string]pulumi.StringOutput{
			"KINDE_DEV_ISSUER_URL":  res.DevIssuerURL,
			"KINDE_PROD_ISSUER_URL": res.ProdIssuerURL,
		},
	})
	return res, registerProjectOidcOutputs(ctx, res)
}

// Private helpers ------------------------------------------------------------

func validateKindeProject(spec Project) error {
	if spec.Platform != nil {
		return nil
	}
	if !hasTenantCreds(spec.Dev) || !hasTenantCreds(spec.Prod) {
		return ErrTenantCredentialsRequired
	}
	return nil
}

func provisionTwoTenantApps(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Project,
	res *ProjectResource,
) error {
	devProv, prodProv, err := resolveProjectTenantProviders(ctx, name, spec, res)
	if err != nil {
		return err
	}
	res.DevProvider = devProv
	res.ProdProvider = prodProv
	appName := defaultKindeStr(spec.AppName, "openbase-"+meta.Project)
	if err := createDevApplication(ctx, name, appName, meta.Project, spec, devProv, res); err != nil {
		return err
	}
	return createProdApplication(ctx, name, appName, meta, spec, prodProv, res)
}

func resolveProjectTenantProviders(
	ctx *pulumi.Context,
	name string,
	spec Project,
	res *ProjectResource,
) (*kindeprov.Provider, *kindeprov.Provider, error) {
	if spec.Platform != nil {
		res.DevIssuerURL = spec.Platform.DevIssuerURL
		res.ProdIssuerURL = spec.Platform.ProdIssuerURL
		res.EmailPasswordConn = spec.Platform.EmailPasswordConn
		return spec.Platform.DevProvider, spec.Platform.ProdProvider, nil
	}
	devIssuer := NormalizeIssuerURL(spec.Dev.Domain)
	prodIssuer := NormalizeIssuerURL(spec.Prod.Domain)
	devProv, err := createTenantProvider(ctx, name+"-dev-prov", devIssuer, spec.Dev, res)
	if err != nil {
		return nil, nil, err
	}
	prodProv, err := createTenantProvider(ctx, name+"-prod-prov", prodIssuer, spec.Prod, res)
	if err != nil {
		return nil, nil, err
	}
	res.DevIssuerURL = pulumi.String(devIssuer).ToStringOutput()
	res.ProdIssuerURL = pulumi.String(prodIssuer).ToStringOutput()
	return devProv, prodProv, nil
}

func createDevApplication(
	ctx *pulumi.Context,
	name, appName, project string,
	spec Project,
	prov *kindeprov.Provider,
	res *ProjectResource,
) error {
	redirects, logouts := buildDevCallbacks(project, spec)
	appOpts := buildKindeAppOptions(res, prov, spec.ImportDevAppID, spec.RetainApplicationsOnDelete)
	app, err := kindeprov.NewApplication(ctx, name+"-dev-app", &kindeprov.ApplicationArgs{
		Name:         pulumi.String(appName),
		Type:         pulumi.String("reg"),
		RedirectUris: pulumi.ToStringArray(redirects),
		LogoutUris:   pulumi.ToStringArray(logouts),
	}, appOpts...)
	if err != nil {
		return err
	}
	res.DevApplication = app
	res.DevAppID = app.ID().ToStringOutput()
	res.DevClientID = app.ClientId
	res.DevClientSecret = pulumi.ToSecret(app.ClientSecret).(pulumi.StringOutput)
	return attachDevEmailPassword(ctx, name, spec, prov, app, res)
}

func attachDevEmailPassword(
	ctx *pulumi.Context,
	name string,
	spec Project,
	prov *kindeprov.Provider,
	app *kindeprov.Application,
	res *ProjectResource,
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
	ac, err := kindeprov.NewApplicationConnection(ctx, name+"-dev-email-pw", &kindeprov.ApplicationConnectionArgs{
		ApplicationId: app.ID(),
		ConnectionId:  pulumi.String(connID),
	}, pulumi.Parent(res), pulumi.Provider(prov), pulumi.RetainOnDelete(true))
	if err != nil {
		return err
	}
	res.DevEmailConn = ac
	return nil
}

func createProdApplication(
	ctx *pulumi.Context,
	name, appName string,
	meta core.ResourceMeta,
	spec Project,
	prov *kindeprov.Provider,
	res *ProjectResource,
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

func buildDevCallbacks(project string, spec Project) ([]string, []string) {
	cb := ensureLeadingSlash(defaultKindeStr(spec.CallbackPath, "/api/auth/kinde_callback"))
	redirects := []string{"http://localhost:5173" + cb, "http://localhost:3000" + cb}
	logouts := []string{"http://localhost:5173/", "http://localhost:3000/"}
	vProj := defaultKindeStr(spec.VercelProjectName, project)
	for _, host := range devCallbackHosts(project, vProj, spec) {
		origin := NormalizeIssuerURL(host)
		redirects = append(redirects, origin+cb)
		logouts = append(logouts, origin+"/")
	}
	redirects = append(redirects, spec.ExtraDevRedirectURIs...)
	logouts = append(logouts, spec.ExtraDevLogoutURIs...)
	return sortAndCompact(redirects), sortAndCompact(logouts)
}

func devCallbackHosts(project, vProj string, spec Project) []string {
	hosts := devVercelHosts(vProj, spec.VercelTeamSlug)
	if base := core.NormalizeDomain(spec.BaseDomain); base != "" && project != "" {
		hosts = append(hosts, "https://"+project+"-*."+base)
	}
	for _, d := range core.DedupeDomains([]string{spec.DevDomain}, spec.ExtraDevDomains) {
		hosts = append(hosts, "https://"+d)
	}
	return hosts
}

func devVercelHosts(vProj, teamSlug string) []string {
	if vProj == "" || teamSlug == "" {
		return nil
	}
	return []string{
		"https://" + vProj + ".vercel.app",
		"https://" + vProj + "-" + teamSlug + ".vercel.app",
		"https://" + vProj + "-*-" + teamSlug + ".vercel.app",
	}
}

func buildProdCallbacks(project string, spec Project) ([]string, []string) {
	cb := ensureLeadingSlash(defaultKindeStr(spec.CallbackPath, "/api/auth/kinde_callback"))
	vProj := defaultKindeStr(spec.VercelProjectName, project)
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

func prodVercelAndCustomHosts(project, vProj string, spec Project) []string {
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

func appendProdFallbackOrExplicit(hosts []string, vProj string, spec Project) []string {
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

func registerProjectOidcOutputs(ctx *pulumi.Context, res *ProjectResource) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"devIssuerUrl":      res.DevIssuerURL,
		"devAppId":          res.DevAppID,
		"devClientId":       res.DevClientID,
		"devClientSecret":   res.DevClientSecret,
		"prodIssuerUrl":     res.ProdIssuerURL,
		"prodAppId":         res.ProdAppID,
		"prodClientId":      res.ProdClientID,
		"prodClientSecret":  res.ProdClientSecret,
		"emailPasswordConn": pulumi.String(res.EmailPasswordConn),
	})
}
