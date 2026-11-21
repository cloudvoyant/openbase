// Package kinde provides OpenBase Pulumi components for Kinde OIDC authentication across Account, Project, and OidcApp tiers.
package kinde

import (
	"errors"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	kindeprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-kinde-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.AccountResource = (*Account)(nil)
	_ AccountInput         = (*Account)(nil)
	_ AccountInput         = (*AccountRef)(nil)
)

// ErrEnvironmentCredentialsRequired reports missing nonProd or prod Kinde environment M2M credentials.
var ErrEnvironmentCredentialsRequired = errors.New("kinde: both nonProd and prod environment domain, clientId, and clientSecret are required")

// AccountInput is satisfied by in-memory *Account handles and cross-stack *AccountRef lookups.
type AccountInput interface {
	kindeAccountValues() kindeAccountData
}

// EnvironmentCredentials configures M2M management credentials for a single Kinde environment (Non-Prod or Production).
type EnvironmentCredentials struct {
	Domain       string
	ClientID     pulumi.StringInput
	ClientSecret pulumi.StringInput
	Audience     string
}

// AccountArgs configures a Kinde Business account's nonProd and prod environment connections in infra/platform.
type AccountArgs struct {
	Org                   string
	NonProd               EnvironmentCredentials
	Prod                  EnvironmentCredentials
	BaseDomain            string
	SkipEmailPasswordConn bool
}

// Account holds Kinde environment providers, issuer URLs, and discovered connection IDs.
type Account struct {
	pulumi.ResourceState
	core.AccountMeta

	NonProdProvider   *kindeprov.Provider
	ProdProvider      *kindeprov.Provider
	NonProdIssuerURL  pulumi.StringOutput
	ProdIssuerURL     pulumi.StringOutput
	BaseDomain        pulumi.StringOutput
	EmailPasswordConn string
	nonProdCreds      EnvironmentCredentials
	prodCreds         EnvironmentCredentials
}

// AccountRef provides typed cross-stack access to a kinde.Account exported in infra/platform.
type AccountRef struct {
	NonProdIssuerURL       pulumi.StringOutput
	ProdIssuerURL          pulumi.StringOutput
	BaseDomain             pulumi.StringOutput
	EmailPasswordConn      pulumi.StringOutput
	NonProdM2MClientID     pulumi.StringOutput
	NonProdM2MClientSecret pulumi.StringOutput
	ProdM2MClientID        pulumi.StringOutput
	ProdM2MClientSecret    pulumi.StringOutput
}

// NewAccount validates nonProd and prod Kinde environments, discovers shared connections, and exports account metadata.
func NewAccount(ctx *pulumi.Context, name string, args *AccountArgs, opts ...pulumi.ResourceOption) (*Account, error) {
	if args == nil || !hasEnvCreds(args.NonProd) || !hasEnvCreds(args.Prod) {
		return nil, ErrEnvironmentCredentialsRequired
	}
	name = defaultKindeStr(name, "kinde")
	res := &Account{
		nonProdCreds: args.NonProd,
		prodCreds:    args.Prod,
	}
	info, err := core.InitAccount(ctx, "openbase:kinde:Account", "kinde-account", res, &res.AccountMeta, core.AccountSpec{
		Name: name,
		Org:  args.Org,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionKindeAccount(ctx, name, args, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, buildKindeAccountLink(info, args, res))
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"nonProdIssuerUrl":  res.NonProdIssuerURL,
		"prodIssuerUrl":     res.ProdIssuerURL,
		"baseDomain":        res.BaseDomain,
		"emailPasswordConn": pulumi.String(res.EmailPasswordConn),
	})
}

// LookupAccount resolves a kinde.Account from an infra/platform ScopeRef (by unique kind or explicit name).
func LookupAccount(plat *core.ScopeRef, name ...string) *AccountRef {
	if plat == nil {
		return nil
	}
	return &AccountRef{
		NonProdIssuerURL:       plat.ManifestAttr("kinde-account", name, "nonProdIssuerUrl"),
		ProdIssuerURL:          plat.ManifestAttr("kinde-account", name, "prodIssuerUrl"),
		BaseDomain:             plat.ManifestAttr("kinde-account", name, "baseDomain"),
		EmailPasswordConn:      plat.ManifestAttr("kinde-account", name, "emailPasswordConn"),
		NonProdM2MClientID:     plat.ManifestAttr("kinde-account", name, "kindeNonProdM2mClientId"),
		NonProdM2MClientSecret: pulumi.ToSecret(plat.ManifestAttr("kinde-account", name, "kindeNonProdM2mClientSecret")).(pulumi.StringOutput),
		ProdM2MClientID:        plat.ManifestAttr("kinde-account", name, "kindeProdM2mClientId"),
		ProdM2MClientSecret:    pulumi.ToSecret(plat.ManifestAttr("kinde-account", name, "kindeProdM2mClientSecret")).(pulumi.StringOutput),
	}
}

// NormalizeIssuerURL ensures a Kinde environment domain starts with https:// and has no trailing slash.
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

type kindeAccountData struct {
	local             *Account
	nonProdIssuer     pulumi.StringOutput
	prodIssuer        pulumi.StringOutput
	nonProdClientID   pulumi.StringOutput
	nonProdSecret     pulumi.StringOutput
	prodClientID      pulumi.StringOutput
	prodSecret        pulumi.StringOutput
	emailPasswordConn pulumi.StringOutput
}

func (a *Account) kindeAccountValues() kindeAccountData {
	if a == nil {
		return kindeAccountData{}
	}
	return kindeAccountData{
		local:             a,
		nonProdIssuer:     a.NonProdIssuerURL,
		prodIssuer:        a.ProdIssuerURL,
		emailPasswordConn: pulumi.String(a.EmailPasswordConn).ToStringOutput(),
	}
}

func (r *AccountRef) kindeAccountValues() kindeAccountData {
	if r == nil {
		return kindeAccountData{}
	}
	return kindeAccountData{
		nonProdIssuer:     r.NonProdIssuerURL,
		prodIssuer:        r.ProdIssuerURL,
		nonProdClientID:   r.NonProdM2MClientID,
		nonProdSecret:     r.NonProdM2MClientSecret,
		prodClientID:      r.ProdM2MClientID,
		prodSecret:        r.ProdM2MClientSecret,
		emailPasswordConn: r.EmailPasswordConn,
	}
}

func hasEnvCreds(c EnvironmentCredentials) bool {
	return strings.TrimSpace(c.Domain) != "" && c.ClientID != nil && c.ClientSecret != nil
}

func buildKindeAccountLink(info core.AccountInfo, args *AccountArgs, res *Account) core.LinkDescriptor {
	nonProdSec := pulumi.ToSecret(args.NonProd.ClientSecret.ToStringOutput()).(pulumi.StringOutput)
	prodSec := pulumi.ToSecret(args.Prod.ClientSecret.ToStringOutput()).(pulumi.StringOutput)
	return core.LinkDescriptor{
		Name: info.Name,
		Kind: info.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"nonProdIssuerUrl":            res.NonProdIssuerURL,
			"prodIssuerUrl":               res.ProdIssuerURL,
			"baseDomain":                  res.BaseDomain,
			"emailPasswordConn":           pulumi.String(res.EmailPasswordConn).ToStringOutput(),
			"kindeNonProdM2mClientId":     args.NonProd.ClientID.ToStringOutput(),
			"kindeNonProdM2mClientSecret": nonProdSec,
			"kindeProdM2mClientId":        args.Prod.ClientID.ToStringOutput(),
			"kindeProdM2mClientSecret":    prodSec,
		},
		StackExports: map[string]pulumi.Input{
			"nonProdIssuerUrl":            res.NonProdIssuerURL,
			"prodIssuerUrl":               res.ProdIssuerURL,
			"emailPasswordConn":           pulumi.String(res.EmailPasswordConn),
			"kindeNonProdM2mClientId":     args.NonProd.ClientID,
			"kindeNonProdM2mClientSecret": nonProdSec,
			"kindeProdM2mClientId":        args.Prod.ClientID,
			"kindeProdM2mClientSecret":    prodSec,
		},
	}
}

func provisionKindeAccount(ctx *pulumi.Context, name string, args *AccountArgs, res *Account) error {
	nonProdIssuer := NormalizeIssuerURL(args.NonProd.Domain)
	prodIssuer := NormalizeIssuerURL(args.Prod.Domain)
	nonProdProv, err := createEnvProvider(ctx, name+"-nonprod-prov", nonProdIssuer, args.NonProd, res)
	if err != nil {
		return err
	}
	prodProv, err := createEnvProvider(ctx, name+"-prod-prov", prodIssuer, args.Prod, res)
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

func createEnvProvider(
	ctx *pulumi.Context,
	name, issuer string,
	creds EnvironmentCredentials,
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
