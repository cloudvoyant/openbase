// Package kinde provides OpenBase two-tenant Pulumi components for Kinde OIDC authentication across Platform, Project, and OidcApp tiers.
package kinde

import (
	"errors"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	kindeprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-kinde-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.PlatformResource = (*Platform)(nil)

// ErrTenantCredentialsRequired reports missing nonProd or prod Kinde tenant M2M credentials.
var ErrTenantCredentialsRequired = errors.New("kinde: both nonProd and prod tenant domain, clientId, and clientSecret are required")

// TenantCredentials configures M2M management credentials for a single Kinde tenant.
type TenantCredentials struct {
	Domain       string
	ClientID     pulumi.StringInput
	ClientSecret pulumi.StringInput
	Audience     string
}

// PlatformArgs configures organization-level Kinde nonProd and prod tenant connections provisioned once per organization.
type PlatformArgs struct {
	Org                   string
	NonProd               TenantCredentials
	Prod                  TenantCredentials
	BaseDomain            string
	SkipEmailPasswordConn bool
}

// Platform holds organization-level Kinde tenant providers, issuer URLs, and discovered connection IDs.
type Platform struct {
	pulumi.ResourceState
	core.PlatformMeta

	NonProdProvider   *kindeprov.Provider
	ProdProvider      *kindeprov.Provider
	NonProdIssuerURL  pulumi.StringOutput
	ProdIssuerURL     pulumi.StringOutput
	BaseDomain        pulumi.StringOutput
	EmailPasswordConn string
}

// NewPlatform validates nonProd/prod Kinde tenants, discovers shared connections, and exports tenant metadata.
func NewPlatform(ctx *pulumi.Context, name string, args *PlatformArgs, opts ...pulumi.ResourceOption) (*Platform, error) {
	if args == nil || !hasTenantCreds(args.NonProd) || !hasTenantCreds(args.Prod) {
		return nil, ErrTenantCredentialsRequired
	}
	name = defaultKindeStr(name, "kinde-platform")
	res := &Platform{}
	info, err := core.InitPlatform(ctx, "openbase:kinde:Platform", "kinde-platform", res, &res.PlatformMeta, core.PlatformSpec{
		Name: name,
		Org:  args.Org,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionKindePlatform(ctx, name, args, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, core.LinkDescriptor{
		Name: info.Name,
		Kind: info.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"nonProdIssuerUrl":  res.NonProdIssuerURL,
			"prodIssuerUrl":     res.ProdIssuerURL,
			"baseDomain":        res.BaseDomain,
			"emailPasswordConn": pulumi.String(res.EmailPasswordConn).ToStringOutput(),
		},
		StackExports: map[string]pulumi.Input{
			"nonProdIssuerUrl":            res.NonProdIssuerURL,
			"prodIssuerUrl":               res.ProdIssuerURL,
			"emailPasswordConn":           pulumi.String(res.EmailPasswordConn),
			"kindeNonProdM2mClientId":     args.NonProd.ClientID,
			"kindeNonProdM2mClientSecret": pulumi.ToSecret(args.NonProd.ClientSecret.ToStringOutput()),
			"kindeProdM2mClientId":        args.Prod.ClientID,
			"kindeProdM2mClientSecret":    pulumi.ToSecret(args.Prod.ClientSecret.ToStringOutput()),
		},
	})
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"nonProdIssuerUrl":  res.NonProdIssuerURL,
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

func provisionKindePlatform(ctx *pulumi.Context, name string, args *PlatformArgs, res *Platform) error {
	nonProdIssuer := NormalizeIssuerURL(args.NonProd.Domain)
	prodIssuer := NormalizeIssuerURL(args.Prod.Domain)
	nonProdProv, err := createTenantProvider(ctx, name+"-nonprod-prov", nonProdIssuer, args.NonProd, res)
	if err != nil {
		return err
	}
	prodProv, err := createTenantProvider(ctx, name+"-prod-prov", prodIssuer, args.Prod, res)
	if err != nil {
		return err
	}
	res.NonProdProvider = nonProdProv
	res.ProdProvider = prodProv
	res.NonProdIssuerURL = pulumi.String(nonProdIssuer).ToStringOutput()
	res.ProdIssuerURL = pulumi.String(prodIssuer).ToStringOutput()
	res.BaseDomain = pulumi.String(core.NormalizeDomain(args.BaseDomain)).ToStringOutput()
	if args.SkipEmailPasswordConn {
		return nil
	}
	conns, err := kindeprov.GetConnections(ctx, nil, pulumi.Provider(nonProdProv))
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
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}
