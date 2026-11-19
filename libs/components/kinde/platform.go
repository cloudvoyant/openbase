// Package kinde provides OpenBase two-tenant Pulumi components for Kinde OIDC authentication across Platform, Project, and OidcApp tiers.
package kinde

import (
	"errors"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	kindeprov "github.com/cloudvoyant/openbase/internal/providers/kinde"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*PlatformResource)(nil)

// ErrTenantCredentialsRequired reports missing dev or prod Kinde tenant M2M credentials.
var ErrTenantCredentialsRequired = errors.New("kinde: both dev and prod tenant domain, clientId, and clientSecret are required")

// TenantCredentials configures M2M management credentials for a single Kinde tenant.
type TenantCredentials struct {
	Domain       string
	ClientID     pulumi.StringInput
	ClientSecret pulumi.StringInput
	Audience     string
}

// Platform declares organization-level Kinde dev and prod tenant connections provisioned once per organization.
type Platform struct {
	Name                  string
	Project               string
	Repo                  string
	Environment           string
	Dev                   TenantCredentials
	Prod                  TenantCredentials
	BaseDomain            string
	SkipEmailPasswordConn bool
}

// PlatformResource holds organization-level Kinde tenant providers, issuer URLs, and discovered connection IDs.
type PlatformResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	DevProvider       *kindeprov.Provider
	ProdProvider      *kindeprov.Provider
	DevIssuerURL      pulumi.StringOutput
	ProdIssuerURL     pulumi.StringOutput
	BaseDomain        pulumi.StringOutput
	EmailPasswordConn string
}

// Register validates dev/prod Kinde tenants, discovers shared connections, and exports tenant metadata.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	if !hasTenantCreds(p.Dev) || !hasTenantCreds(p.Prod) {
		return nil, ErrTenantCredentialsRequired
	}
	p.Name = defaultKindeStr(p.Name, "kinde-platform")
	p.Project = defaultKindeStr(p.Project, "platform")
	env := defaultKindeStr(p.Environment, string(core.EnvShared))
	res := &PlatformResource{}
	meta, err := core.InitStateless(ctx, "openbase:kinde:Platform", "kinde-platform", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        p.Name,
		Project:     p.Project,
		Repo:        p.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionKindePlatform(ctx, p.Name, p, res); err != nil {
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
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"devIssuerUrl":      res.DevIssuerURL,
		"prodIssuerUrl":     res.ProdIssuerURL,
		"baseDomain":        res.BaseDomain,
		"emailPasswordConn": pulumi.String(res.EmailPasswordConn),
	})
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

func hasTenantCreds(c TenantCredentials) bool {
	return strings.TrimSpace(c.Domain) != "" && c.ClientID != nil && c.ClientSecret != nil
}

func provisionKindePlatform(ctx *pulumi.Context, name string, spec Platform, res *PlatformResource) error {
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
	res.BaseDomain = pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput()
	if spec.SkipEmailPasswordConn {
		return nil
	}
	conns, err := kindeprov.GetConnections(ctx, nil, pulumi.Provider(devProv))
	if err != nil {
		return err
	}
	res.EmailPasswordConn = findEmailPasswordConn(conns.Connections)
	return nil
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

func findEmailPasswordConn(conns []kindeprov.GetConnectionsConnection) string {
	for _, c := range conns {
		if c.Strategy == "email:password" {
			return c.Id
		}
	}
	return ""
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
