// Package kinde provides OpenBase Pulumi components for Kinde OIDC authentication across Account, Project, and OidcApp tiers.
package kinde

import (
	"errors"
	"fmt"
	"slices"
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

const (
	// DefaultNonProdEnvironment is the default Kinde environment key for dev and preview stacks.
	DefaultNonProdEnvironment = "non-prod"
	// DefaultProdEnvironment is the default Kinde environment key for stage and prod stacks.
	DefaultProdEnvironment = "prod"
)

// ErrEnvironmentCredentialsRequired reports missing or incomplete Kinde environment M2M credentials.
var ErrEnvironmentCredentialsRequired = errors.New("kinde: at least one environment with domain, clientId, and clientSecret is required")

// AccountInput is satisfied by in-memory *Account handles and cross-stack *AccountRef lookups.
type AccountInput interface {
	kindeAccountValues() kindeAccountData
}

// EnvironmentCredentials configures M2M management credentials for a single Kinde environment.
type EnvironmentCredentials struct {
	// Domain is the Kinde environment hostname (with or without https://).
	Domain string
	// ClientID is the Kinde Management API M2M application client ID.
	ClientID pulumi.StringInput
	// ClientSecret is the Kinde Management API M2M application client secret.
	ClientSecret pulumi.StringInput
	// Audience overrides the default <issuer>/api Management API token audience.
	Audience string
}

// EnvironmentRef provides typed cross-stack access to a named Kinde environment exported by kinde.Account.
type EnvironmentRef struct {
	// IssuerURL is the normalized HTTPS issuer URL for the Kinde environment.
	IssuerURL pulumi.StringOutput
	// M2MClientID is the Kinde Management API client ID for the environment.
	M2MClientID pulumi.StringOutput
	// M2MClientSecret is the encrypted Kinde Management API client secret for the environment.
	M2MClientSecret pulumi.StringOutput
}

// AccountArgs configures a Kinde Business account's named environment connections in infra/platform.
type AccountArgs struct {
	// Org records the organization slug in openbase.io/* resource tags.
	Org string
	// Environments maps arbitrary Kinde environment names (for example "non-prod", "prod", "staging") to M2M credentials.
	Environments map[string]EnvironmentCredentials
	// BaseDomain records the optional default base domain for downstream Kinde projects.
	BaseDomain string
	// SkipEmailPasswordConn disables querying Kinde for the email:password connection ID during Account creation.
	SkipEmailPasswordConn bool
}

// Account holds Kinde environment providers, issuer URLs, and discovered connection IDs.
type Account struct {
	pulumi.ResourceState
	core.AccountMeta

	// Providers maps each configured Kinde environment name to its Pulumi kinde.Provider resource.
	Providers map[string]*kindeprov.Provider
	// IssuerURLs maps each configured Kinde environment name to its normalized HTTPS issuer URL output.
	IssuerURLs map[string]pulumi.StringOutput
	// NonProdProvider is the convenience provider handle for DefaultNonProdEnvironment ("non-prod") when configured.
	NonProdProvider *kindeprov.Provider
	// ProdProvider is the convenience provider handle for DefaultProdEnvironment ("prod") when configured.
	ProdProvider *kindeprov.Provider
	// NonProdIssuerURL is the convenience issuer output for DefaultNonProdEnvironment ("non-prod") when configured.
	NonProdIssuerURL pulumi.StringOutput
	// ProdIssuerURL is the convenience issuer output for DefaultProdEnvironment ("prod") when configured.
	ProdIssuerURL pulumi.StringOutput
	// BaseDomain is the normalized optional base domain exported for downstream projects.
	BaseDomain pulumi.StringOutput
	// EmailPasswordConn is the discovered Kinde email:password connection ID.
	EmailPasswordConn string
	envCreds          map[string]EnvironmentCredentials
}

// AccountRef provides typed cross-stack access to a kinde.Account exported in infra/platform.
type AccountRef struct {
	// NonProdIssuerURL is the issuer URL output for the default "non-prod" Kinde environment.
	NonProdIssuerURL pulumi.StringOutput
	// ProdIssuerURL is the issuer URL output for the default "prod" Kinde environment.
	ProdIssuerURL pulumi.StringOutput
	// BaseDomain is the normalized optional base domain exported by the account.
	BaseDomain pulumi.StringOutput
	// EmailPasswordConn is the discovered email:password connection ID exported by the account.
	EmailPasswordConn pulumi.StringOutput

	plat  *core.ScopeRef
	alias []string
}

// NewAccount validates named Kinde environments, discovers shared connections, and exports account metadata.
func NewAccount(ctx *pulumi.Context, name string, args *AccountArgs, opts ...pulumi.ResourceOption) (*Account, error) {
	if args == nil || !hasValidEnvMap(args.Environments) {
		return nil, ErrEnvironmentCredentialsRequired
	}
	name = defaultKindeStr(name, "kinde")
	res := &Account{
		Providers:  make(map[string]*kindeprov.Provider, len(args.Environments)),
		IssuerURLs: make(map[string]pulumi.StringOutput, len(args.Environments)),
		envCreds:   args.Environments,
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

// LookupAccount resolves a kinde.Account by alias name from an infra/platform ScopeRef.
func LookupAccount(plat *core.ScopeRef, name string) *AccountRef {
	if plat == nil {
		return nil
	}
	alias := []string{name}
	return &AccountRef{
		NonProdIssuerURL:  plat.ManifestAttr("kinde-account", alias, envIssuerKey(DefaultNonProdEnvironment), "nonProdIssuerUrl"),
		ProdIssuerURL:     plat.ManifestAttr("kinde-account", alias, envIssuerKey(DefaultProdEnvironment), "prodIssuerUrl"),
		BaseDomain:        plat.ManifestAttr("kinde-account", alias, "baseDomain"),
		EmailPasswordConn: plat.ManifestAttr("kinde-account", alias, "emailPasswordConn"),
		plat:              plat,
		alias:             alias,
	}
}

// Environment resolves a named Kinde environment's issuer and encrypted M2M credentials from the platform manifest.
func (r *AccountRef) Environment(envName string) EnvironmentRef {
	if r == nil || r.plat == nil {
		return EnvironmentRef{}
	}
	issuerFallback, idFallback, secFallback := legacyEnvExportKeys(envName)
	return EnvironmentRef{
		IssuerURL:       r.plat.ManifestAttr("kinde-account", r.alias, envIssuerKey(envName), issuerFallback),
		M2MClientID:     r.plat.ManifestAttr("kinde-account", r.alias, envClientIDKey(envName), idFallback),
		M2MClientSecret: pulumi.ToSecret(r.plat.ManifestAttr("kinde-account", r.alias, envClientSecretKey(envName), secFallback)).(pulumi.StringOutput),
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
	ref               *AccountRef
	emailPasswordConn pulumi.StringOutput
}

func (a *Account) kindeAccountValues() kindeAccountData {
	if a == nil {
		return kindeAccountData{}
	}
	return kindeAccountData{
		local:             a,
		emailPasswordConn: pulumi.String(a.EmailPasswordConn).ToStringOutput(),
	}
}

func (r *AccountRef) kindeAccountValues() kindeAccountData {
	if r == nil {
		return kindeAccountData{}
	}
	return kindeAccountData{
		ref:               r,
		emailPasswordConn: r.EmailPasswordConn,
	}
}

func hasEnvCreds(c EnvironmentCredentials) bool {
	return strings.TrimSpace(c.Domain) != "" && c.ClientID != nil && c.ClientSecret != nil
}

func hasValidEnvMap(envs map[string]EnvironmentCredentials) bool {
	if len(envs) == 0 {
		return false
	}
	for k, c := range envs {
		if strings.TrimSpace(k) == "" || !hasEnvCreds(c) {
			return false
		}
	}
	return true
}

func sortedEnvKeys(envs map[string]EnvironmentCredentials) []string {
	keys := make([]string, 0, len(envs))
	for k := range envs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func envIssuerKey(envName string) string       { return envName + ":issuerUrl" }
func envClientIDKey(envName string) string     { return envName + ":m2mClientId" }
func envClientSecretKey(envName string) string { return envName + ":m2mClientSecret" }

func legacyEnvExportKeys(envName string) (string, string, string) {
	switch envName {
	case DefaultNonProdEnvironment:
		return "nonProdIssuerUrl", "kindeNonProdM2mClientId", "kindeNonProdM2mClientSecret"
	case DefaultProdEnvironment:
		return "prodIssuerUrl", "kindeProdM2mClientId", "kindeProdM2mClientSecret"
	default:
		return envIssuerKey(envName), envClientIDKey(envName), envClientSecretKey(envName)
	}
}

func buildKindeAccountLink(info core.AccountInfo, args *AccountArgs, res *Account) core.LinkDescriptor {
	attrs := map[string]pulumi.StringOutput{
		"baseDomain":        res.BaseDomain,
		"emailPasswordConn": pulumi.String(res.EmailPasswordConn).ToStringOutput(),
	}
	exports := map[string]pulumi.Input{
		"emailPasswordConn": pulumi.String(res.EmailPasswordConn),
	}
	for _, envKey := range sortedEnvKeys(args.Environments) {
		creds := args.Environments[envKey]
		sec := pulumi.ToSecret(creds.ClientSecret.ToStringOutput()).(pulumi.StringOutput)
		issuer := res.IssuerURLs[envKey]
		attrs[envIssuerKey(envKey)] = issuer
		attrs[envClientIDKey(envKey)] = creds.ClientID.ToStringOutput()
		attrs[envClientSecretKey(envKey)] = sec
		addLegacyKindeExports(envKey, issuer, creds.ClientID.ToStringOutput(), sec, attrs, exports)
	}
	return core.LinkDescriptor{
		Name:         info.Name,
		Kind:         info.Kind,
		Attributes:   attrs,
		StackExports: exports,
	}
}

func addLegacyKindeExports(
	envKey string,
	issuer, clientID, secret pulumi.StringOutput,
	attrs map[string]pulumi.StringOutput,
	exports map[string]pulumi.Input,
) {
	if envKey != DefaultNonProdEnvironment && envKey != DefaultProdEnvironment {
		return
	}
	iKey, idKey, secKey := legacyEnvExportKeys(envKey)
	attrs[iKey] = issuer
	attrs[idKey] = clientID
	attrs[secKey] = secret
	exports[iKey] = issuer
	exports[idKey] = clientID
	exports[secKey] = secret
}

func provisionKindeAccount(ctx *pulumi.Context, name string, args *AccountArgs, res *Account) error {
	keys := sortedEnvKeys(args.Environments)
	for _, envKey := range keys {
		creds := args.Environments[envKey]
		issuer := NormalizeIssuerURL(creds.Domain)
		prov, err := createEnvProvider(ctx, fmt.Sprintf("%s-%s-prov", name, envKey), issuer, creds, res)
		if err != nil {
			return err
		}
		res.Providers[envKey] = prov
		res.IssuerURLs[envKey] = pulumi.String(issuer).ToStringOutput()
	}
	res.NonProdProvider = res.Providers[DefaultNonProdEnvironment]
	res.ProdProvider = res.Providers[DefaultProdEnvironment]
	res.NonProdIssuerURL = res.IssuerURLs[DefaultNonProdEnvironment]
	res.ProdIssuerURL = res.IssuerURLs[DefaultProdEnvironment]
	res.BaseDomain = pulumi.String(core.NormalizeDomain(args.BaseDomain)).ToStringOutput()
	return discoverEmailPasswordConn(ctx, args, keys, res)
}

func discoverEmailPasswordConn(ctx *pulumi.Context, args *AccountArgs, keys []string, res *Account) error {
	if args.SkipEmailPasswordConn {
		return nil
	}
	prov := res.NonProdProvider
	if prov == nil && len(keys) > 0 {
		prov = res.Providers[keys[0]]
	}
	if prov == nil {
		return nil
	}
	conns, err := kindeprov.GetConnections(ctx, nil, pulumi.Provider(prov))
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
