// Package kinde provides OpenBase two-tenant Pulumi components for Kinde OIDC authentication.
package kinde

import (
	"errors"
	"slices"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	kindeprov "github.com/cloudvoyant/openbase/internal/providers/kinde"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*SharedOidcAppResource)(nil)

// ErrTenantCredentialsRequired reports missing dev or prod Kinde tenant M2M credentials.
var ErrTenantCredentialsRequired = errors.New("kinde: both dev and prod tenant domain, clientId, and clientSecret are required")

// TenantCredentials configures M2M management credentials for a single Kinde tenant.
type TenantCredentials struct {
	Domain       string
	ClientID     pulumi.StringInput
	ClientSecret pulumi.StringInput
	Audience     string
}

// SharedOidcApp declares two-tenant Kinde OIDC Applications in the "shared" stack.
type SharedOidcApp struct {
	Name                       string
	Project                    string
	Repo                       string
	Environment                string
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

// SharedOidcAppResource is the provisioned handle for dev and prod Kinde OIDC Applications.
type SharedOidcAppResource struct {
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

// Register provisions dev and prod Kinde OIDC Applications and exports credentials for StackReference.
func (s SharedOidcApp) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*SharedOidcAppResource, error) {
	if err := validateSharedOidcApp(s); err != nil {
		return nil, err
	}
	s.Name = defaultKindeStr(s.Name, "shared")
	env := defaultKindeStr(s.Environment, string(core.EnvShared))
	res := &SharedOidcAppResource{}
	meta, err := core.InitStateless(ctx, "openbase:kinde:SharedOidcApp", "kinde-shared-oidc", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        s.Name,
		Project:     s.Project,
		Repo:        s.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionTwoTenantApps(ctx, s.Name, meta, s, res); err != nil {
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
	if err := registerSharedOidcOutputs(ctx, res); err != nil {
		return nil, err
	}
	return res, nil
}

// NormalizeIssuerURL ensures a Kinde tenant domain starts with https:// and has no trailing slash.
func NormalizeIssuerURL(raw string) string {
	trimmed := strings.TrimSpace(strings.TrimRight(raw, "/"))
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "https://") && !strings.HasPrefix(trimmed, "http://") {
		return "https://" + trimmed
	}
	return trimmed
}

// Private helpers ------------------------------------------------------------

func validateSharedOidcApp(spec SharedOidcApp) error {
	if !hasTenantCreds(spec.Dev) || !hasTenantCreds(spec.Prod) {
		return ErrTenantCredentialsRequired
	}
	return nil
}

func hasTenantCreds(c TenantCredentials) bool {
	return strings.TrimSpace(c.Domain) != "" && c.ClientID != nil && c.ClientSecret != nil
}

func provisionTwoTenantApps(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec SharedOidcApp,
	res *SharedOidcAppResource,
) error {
	devIssuer := NormalizeIssuerURL(spec.Dev.Domain)
	prodIssuer := NormalizeIssuerURL(spec.Prod.Domain)
	devProv, err := createTenantProvider(ctx, name+"-dev-prov", devIssuer, spec.Dev, res)
	if err != nil {
		return err
	}
	prodProv, err := createTenantProvider(ctx, name+"-prod-prov", prodIssuer, spec.Prod, res)
	if err != nil {
		return err
	}
	res.DevProvider = devProv
	res.ProdProvider = prodProv
	res.DevIssuerURL = pulumi.String(devIssuer).ToStringOutput()
	res.ProdIssuerURL = pulumi.String(prodIssuer).ToStringOutput()

	appName := defaultKindeStr(spec.AppName, "openbase-"+meta.Project)
	if err := createDevApplication(ctx, name, appName, meta.Project, spec, devProv, res); err != nil {
		return err
	}
	return createProdApplication(ctx, name, appName, meta, spec, prodProv, res)
}

func createTenantProvider(
	ctx *pulumi.Context,
	name, issuer string,
	creds TenantCredentials,
	parent pulumi.Resource,
) (*kindeprov.Provider, error) {
	aud := defaultKindeStr(creds.Audience, issuer+"/api")
	return kindeprov.NewProvider(ctx, name, &kindeprov.ProviderArgs{
		Domain:       pulumi.String(issuer),
		Audience:     pulumi.String(aud),
		ClientId:     creds.ClientID.ToStringOutput(),
		ClientSecret: pulumi.ToSecret(creds.ClientSecret.ToStringOutput()).(pulumi.StringOutput),
	}, pulumi.Parent(parent))
}

func createDevApplication(
	ctx *pulumi.Context,
	name, appName, project string,
	spec SharedOidcApp,
	prov *kindeprov.Provider,
	res *SharedOidcAppResource,
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
	spec SharedOidcApp,
	prov *kindeprov.Provider,
	app *kindeprov.Application,
	res *SharedOidcAppResource,
) error {
	if spec.SkipEmailPasswordConn {
		return nil
	}
	conns, err := kindeprov.GetConnections(ctx, nil, pulumi.Provider(prov))
	if err != nil {
		return err
	}
	connID := findEmailPasswordConn(conns.Connections)
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

func findEmailPasswordConn(conns []kindeprov.GetConnectionsConnection) string {
	for _, c := range conns {
		if c.Strategy == "email:password" {
			return c.Id
		}
	}
	return ""
}

func createProdApplication(
	ctx *pulumi.Context,
	name, appName string,
	meta core.ResourceMeta,
	spec SharedOidcApp,
	prov *kindeprov.Provider,
	res *SharedOidcAppResource,
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

func buildDevCallbacks(project string, spec SharedOidcApp) ([]string, []string) {
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

func devCallbackHosts(project, vProj string, spec SharedOidcApp) []string {
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

func buildProdCallbacks(project string, spec SharedOidcApp) ([]string, []string) {
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

func prodVercelAndCustomHosts(project, vProj string, spec SharedOidcApp) []string {
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

func appendProdFallbackOrExplicit(hosts []string, vProj string, spec SharedOidcApp) []string {
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

func registerSharedOidcOutputs(ctx *pulumi.Context, res *SharedOidcAppResource) error {
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

func defaultKindeStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

func ensureLeadingSlash(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}
