// Package kinde provides OpenBase two-tenant Pulumi components for Kinde authentication.
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

// ErrTenantCredentialsRequired reports missing dev or prod Kinde tenant M2M credentials.
var ErrTenantCredentialsRequired = errors.New("kinde: both dev and prod tenant domain, clientId, and clientSecret are required")

// TenantCredentials configures M2M management credentials for a single Kinde tenant.
type TenantCredentials struct {
	Domain       string
	ClientID     pulumi.StringInput
	ClientSecret pulumi.StringInput
	Audience     string
}

// SharedAuthArgs configures two-tenant Kinde Applications in the "shared" stack.
type SharedAuthArgs struct {
	Project                    string
	Repo                       string
	Environment                string
	AppName                    string
	Dev                        TenantCredentials
	Prod                       TenantCredentials
	VercelProjectName          string
	VercelTeamSlug             string
	StageDomain                string
	ProdDomain                 string
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

// SharedAuth provisions dev and prod Kinde Applications with wildcard preview and domain callbacks.
type SharedAuth struct {
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

// NewSharedAuth creates dev and prod Kinde Applications and exports credentials for StackReference.
func NewSharedAuth(ctx *pulumi.Context, name string, args SharedAuthArgs, opts ...pulumi.ResourceOption) (*SharedAuth, error) {
	if err := validateSharedAuthArgs(args); err != nil {
		return nil, err
	}
	res := &SharedAuth{}
	env := defaultKindeStr(args.Environment, string(core.EnvShared))
	meta, err := core.InitStateless(ctx, "openbase:kinde:SharedAuth", name, "kinde-shared", res, &res.StatelessMeta, core.StatelessResourceArgs{
		Project:     args.Project,
		Repo:        args.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionTwoTenantApps(ctx, name, meta, args, res); err != nil {
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
	if err := registerSharedAuthOutputs(ctx, res); err != nil {
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

func validateSharedAuthArgs(args SharedAuthArgs) error {
	if !hasTenantCreds(args.Dev) || !hasTenantCreds(args.Prod) {
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
	args SharedAuthArgs,
	res *SharedAuth,
) error {
	devIssuer := NormalizeIssuerURL(args.Dev.Domain)
	prodIssuer := NormalizeIssuerURL(args.Prod.Domain)
	devProv, err := createTenantProvider(ctx, name+"-dev-prov", devIssuer, args.Dev, res)
	if err != nil {
		return err
	}
	prodProv, err := createTenantProvider(ctx, name+"-prod-prov", prodIssuer, args.Prod, res)
	if err != nil {
		return err
	}
	res.DevProvider = devProv
	res.ProdProvider = prodProv
	res.DevIssuerURL = pulumi.String(devIssuer).ToStringOutput()
	res.ProdIssuerURL = pulumi.String(prodIssuer).ToStringOutput()

	appName := defaultKindeStr(args.AppName, "openbase-"+meta.Project)
	if err := createDevApplication(ctx, name, appName, args, devProv, res); err != nil {
		return err
	}
	return createProdApplication(ctx, name, appName, meta, args, prodProv, res)
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
	name, appName string,
	args SharedAuthArgs,
	prov *kindeprov.Provider,
	res *SharedAuth,
) error {
	redirects, logouts := buildDevCallbacks(args)
	appOpts := buildKindeAppOptions(res, prov, args.ImportDevAppID, args.RetainApplicationsOnDelete)
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
	return attachDevEmailPassword(ctx, name, args, prov, app, res)
}

func attachDevEmailPassword(
	ctx *pulumi.Context,
	name string,
	args SharedAuthArgs,
	prov *kindeprov.Provider,
	app *kindeprov.Application,
	res *SharedAuth,
) error {
	if args.SkipEmailPasswordConn {
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
	args SharedAuthArgs,
	prov *kindeprov.Provider,
	res *SharedAuth,
) error {
	redirects, logouts := buildProdCallbacks(meta.Project, args)
	appOpts := buildKindeAppOptions(res, prov, args.ImportProdAppID, args.RetainApplicationsOnDelete)
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

func buildDevCallbacks(args SharedAuthArgs) ([]string, []string) {
	cb := ensureLeadingSlash(defaultKindeStr(args.CallbackPath, "/api/auth/kinde_callback"))
	redirects := []string{"http://localhost:5173" + cb, "http://localhost:3000" + cb}
	logouts := []string{"http://localhost:5173/", "http://localhost:3000/"}
	vProj := defaultKindeStr(args.VercelProjectName, args.Project)
	if vProj != "" && args.VercelTeamSlug != "" {
		wildcardHost := "https://" + vProj + "-*-" + args.VercelTeamSlug + ".vercel.app"
		redirects = append(redirects, wildcardHost+cb)
		logouts = append(logouts, wildcardHost+"/")
	}
	redirects = append(redirects, args.ExtraDevRedirectURIs...)
	logouts = append(logouts, args.ExtraDevLogoutURIs...)
	slices.Sort(redirects)
	slices.Sort(logouts)
	return redirects, logouts
}

func buildProdCallbacks(project string, args SharedAuthArgs) ([]string, []string) {
	cb := ensureLeadingSlash(defaultKindeStr(args.CallbackPath, "/api/auth/kinde_callback"))
	vProj := defaultKindeStr(args.VercelProjectName, project)
	prodHost := args.ProdDomain
	if prodHost == "" && args.VercelTeamSlug != "" {
		prodHost = vProj + "-" + args.VercelTeamSlug + ".vercel.app"
	}
	stageHost := args.StageDomain
	if stageHost == "" && args.VercelTeamSlug != "" {
		stageHost = vProj + "-stage-" + args.VercelTeamSlug + ".vercel.app"
	}
	var redirects, logouts []string
	for _, host := range []string{prodHost, stageHost} {
		if host != "" {
			origin := NormalizeIssuerURL(host)
			redirects = append(redirects, origin+cb)
			logouts = append(logouts, origin+"/")
		}
	}
	redirects = append(redirects, args.ExtraProdRedirectURIs...)
	logouts = append(logouts, args.ExtraProdLogoutURIs...)
	slices.Sort(redirects)
	slices.Sort(logouts)
	return redirects, logouts
}

func registerSharedAuthOutputs(ctx *pulumi.Context, res *SharedAuth) error {
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
