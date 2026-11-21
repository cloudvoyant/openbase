// Package vercel provides OpenBase Pulumi components for Vercel across Account, Project, WebApp, and BlobStore tiers.
package vercel

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.AccountResource    = (*Account)(nil)
	_ core.OIDCProviderSource = AccountArgs{}
	_ core.OIDCProviderSource = (*Account)(nil)
	_ AccountInput            = (*Account)(nil)
	_ AccountInput            = (*AccountRef)(nil)
)

// AccountInput is satisfied by in-memory *Account handles and cross-stack *AccountRef lookups.
type AccountInput interface {
	vercelAccountValues() vercelAccountData
}

// AccountArgs configures team-level Vercel account settings provisioned once per organization in infra/platform.
type AccountArgs struct {
	Org            string
	TeamID         string
	TeamSlug       string
	OIDCIssuerMode string
	BaseDomain     string
}

// Account holds team-level Vercel configuration exported for downstream vercel.Project stacks and OIDC federation.
type Account struct {
	pulumi.ResourceState
	core.AccountMeta

	TeamID     pulumi.StringOutput
	TeamSlug   pulumi.StringOutput
	BaseDomain pulumi.StringOutput
	oidcSpec   core.OIDCIssuer
}

// AccountRef provides typed cross-stack access to a vercel.Account exported in infra/platform.
type AccountRef struct {
	TeamID     pulumi.StringOutput
	TeamSlug   pulumi.StringOutput
	BaseDomain pulumi.StringOutput
}

// NewAccount exports team-level Vercel settings for consumption by vercel.Project stacks and linked cloud IAM pools.
func NewAccount(ctx *pulumi.Context, name string, args *AccountArgs, opts ...pulumi.ResourceOption) (*Account, error) {
	if args == nil {
		args = &AccountArgs{}
	}
	name = defaultVercelStr(name, "vercel")
	orgScope := core.OrgScope(args.Org, args.TeamSlug, ctx.Organization())
	issuer, _ := args.OIDCIssuerSpec()
	res := &Account{
		TeamID:     pulumi.String(args.TeamID).ToStringOutput(),
		TeamSlug:   pulumi.String(args.TeamSlug).ToStringOutput(),
		BaseDomain: pulumi.String(core.NormalizeDomain(args.BaseDomain)).ToStringOutput(),
		oidcSpec:   issuer,
	}
	info, err := core.InitAccount(ctx, "openbase:vercel:Account", "vercel-account", res, &res.AccountMeta, core.AccountSpec{
		Name: name,
		Org:  orgScope,
	}, opts...)
	if err != nil {
		return nil, err
	}
	res.PublishLink(ctx, core.LinkDescriptor{
		Name: info.Name,
		Kind: info.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"teamId":     res.TeamID,
			"teamSlug":   res.TeamSlug,
			"baseDomain": res.BaseDomain,
		},
		StackExports: map[string]pulumi.Input{
			"teamId":           res.TeamID,
			"teamSlug":         res.TeamSlug,
			"vercelBaseDomain": res.BaseDomain,
		},
	})
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"teamId":           res.TeamID,
		"teamSlug":         res.TeamSlug,
		"baseDomain":       res.BaseDomain,
		"vercelBaseDomain": res.BaseDomain,
	})
}

// LookupAccount resolves a vercel.Account from an infra/platform ScopeRef (by unique kind or explicit name).
func LookupAccount(plat *core.ScopeRef, name ...string) *AccountRef {
	if plat == nil {
		return nil
	}
	return &AccountRef{
		TeamID:     plat.ManifestAttr("vercel-account", name, "teamId"),
		TeamSlug:   plat.ManifestAttr("vercel-account", name, "teamSlug"),
		BaseDomain: plat.ManifestAttr("vercel-account", name, "baseDomain", "vercelBaseDomain"),
	}
}

// OIDCIssuerSpec publishes Vercel's team or global OIDC issuer descriptor for cross-provider federation.
func (p AccountArgs) OIDCIssuerSpec() (core.OIDCIssuer, bool) {
	return OIDCIssuer(p.TeamSlug, p.OIDCIssuerMode)
}

// OIDCIssuerSpec returns the provisioned Vercel team OIDC issuer descriptor.
func (r *Account) OIDCIssuerSpec() (core.OIDCIssuer, bool) {
	if r == nil {
		return core.OIDCIssuer{}, false
	}
	return r.oidcSpec.OIDCIssuerSpec()
}

// OIDCIssuer returns a core.OIDCIssuer descriptor for a Vercel team slug ("team" or "global" issuer mode).
func OIDCIssuer(teamSlug, issuerMode string) (core.OIDCIssuer, bool) {
	if strings.TrimSpace(teamSlug) == "" {
		return core.OIDCIssuer{}, false
	}
	issuerURI := "https://oidc.vercel.com/" + teamSlug
	if issuerMode == "global" {
		issuerURI = "https://oidc.vercel.com"
	}
	return core.OIDCIssuer{
		ID:                 "vercel",
		DisplayName:        "Vercel OIDC",
		IssuerURI:          issuerURI,
		AllowedAudiences:   []string{"https://vercel.com/" + teamSlug},
		SubjectExpr:        "assertion.sub",
		AttributeCondition: fmt.Sprintf("assertion.owner == '%s'", teamSlug),
		Claims: map[string]string{
			"project_id":  "assertion.project_id",
			"project":     "assertion.project",
			"owner":       "assertion.owner",
			"environment": "assertion.environment",
		},
		PrincipalClaim: "project_id",
	}, true
}

// DeriveBlobBaseURL computes https://<slug>.public.blob.vercel-storage.com from a Vercel store_... ID.
func DeriveBlobBaseURL(storeID pulumi.StringInput) pulumi.StringOutput {
	return storeID.ToStringOutput().ApplyT(func(id string) string {
		slug := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "store_"))
		if slug == "" {
			return ""
		}
		return fmt.Sprintf("https://%s.public.blob.vercel-storage.com", slug)
	}).(pulumi.StringOutput)
}

// Private helpers ------------------------------------------------------------

type vercelAccountData struct {
	teamID     pulumi.StringOutput
	teamSlug   pulumi.StringOutput
	baseDomain pulumi.StringOutput
}

func (a *Account) vercelAccountValues() vercelAccountData {
	if a == nil {
		return vercelAccountData{}
	}
	return vercelAccountData{
		teamID:     a.TeamID,
		teamSlug:   a.TeamSlug,
		baseDomain: a.BaseDomain,
	}
}

func (r *AccountRef) vercelAccountValues() vercelAccountData {
	if r == nil {
		return vercelAccountData{}
	}
	return vercelAccountData{
		teamID:     r.TeamID,
		teamSlug:   r.TeamSlug,
		baseDomain: r.BaseDomain,
	}
}

func defaultVercelStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

func optionalVercelStr(val string) pulumi.StringPtrInput {
	if val == "" {
		return nil
	}
	return pulumi.String(val)
}
