// Package neon provides OpenBase Pulumi components for Neon serverless PostgreSQL across Account, Project, and Database tiers.
package neon

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-neon-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.AccountResource = (*Account)(nil)
	_ AccountInput         = (*Account)(nil)
	_ AccountInput         = (*AccountRef)(nil)
)

// AccountInput is satisfied by in-memory *Account handles and cross-stack *AccountRef lookups.
type AccountInput interface {
	neonAccountValues() neonAccountData
}

// AccountArgs configures organization-level Neon account settings and optional org API keys in infra/platform.
type AccountArgs struct {
	Org             string
	OrgID           string
	DefaultRegionID string
	APIKey          pulumi.StringInput
	APIBaseURL      string
	CreateOrgAPIKey bool
	OrgAPIKeyName   string
}

// Account holds organization-level Neon account defaults exported for per-repo neon.Project stacks.
type Account struct {
	pulumi.ResourceState
	core.AccountMeta

	OrgAPIKey       *neonprov.OrgApiKey
	OrgID           pulumi.StringOutput
	DefaultRegionID pulumi.StringOutput
	apiKey          pulumi.StringInput
	apiBaseURL      string
}

// AccountRef provides typed cross-stack access to a neon.Account exported in infra/platform.
type AccountRef struct {
	OrgID           pulumi.StringOutput
	DefaultRegionID pulumi.StringOutput
}

// NewAccount provisions organization-level Neon account configuration on the platform stack.
func NewAccount(ctx *pulumi.Context, name string, args *AccountArgs, opts ...pulumi.ResourceOption) (*Account, error) {
	if args == nil {
		args = &AccountArgs{}
	}
	name = defaultIdentifier(name, "neon")
	res := &Account{}
	info, err := core.InitAccount(ctx, "openbase:neon:Account", "neon-account", res, &res.AccountMeta, core.AccountSpec{
		Name: name,
		Org:  args.Org,
	}, opts...)
	if err != nil {
		return nil, err
	}
	populateNeonAccountDefaults(args, res)
	if err := createOptionalNeonOrgKey(ctx, name, info, args, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, buildNeonAccountLink(info, res))
	return res, registerNeonAccountOutputs(ctx, res)
}

// LookupAccount resolves a neon.Account by alias name from an infra/platform ScopeRef.
func LookupAccount(plat *core.ScopeRef, name string) *AccountRef {
	if plat == nil {
		return nil
	}
	alias := []string{name}
	return &AccountRef{
		OrgID:           plat.ManifestAttr("neon-account", alias, "neonOrgId"),
		DefaultRegionID: plat.ManifestAttr("neon-account", alias, "neonDefaultRegionId"),
	}
}

// Private helpers ------------------------------------------------------------

type neonAccountData struct {
	orgID           pulumi.StringOutput
	defaultRegionID pulumi.StringOutput
	apiKey          pulumi.StringInput
	apiBaseURL      string
}

func (a *Account) neonAccountValues() neonAccountData {
	if a == nil {
		return neonAccountData{}
	}
	return neonAccountData{
		orgID:           a.OrgID,
		defaultRegionID: a.DefaultRegionID,
		apiKey:          a.apiKey,
		apiBaseURL:      a.apiBaseURL,
	}
}

func (r *AccountRef) neonAccountValues() neonAccountData {
	if r == nil {
		return neonAccountData{}
	}
	return neonAccountData{
		orgID:           r.OrgID,
		defaultRegionID: r.DefaultRegionID,
	}
}

func populateNeonAccountDefaults(args *AccountArgs, res *Account) {
	region := defaultIdentifier(args.DefaultRegionID, "aws-us-east-1")
	res.OrgID = pulumi.String(args.OrgID).ToStringOutput()
	res.DefaultRegionID = pulumi.String(region).ToStringOutput()
	res.apiKey = args.APIKey
	res.apiBaseURL = args.APIBaseURL
}

func createOptionalNeonOrgKey(
	ctx *pulumi.Context,
	name string,
	info core.AccountInfo,
	args *AccountArgs,
	res *Account,
) error {
	if !args.CreateOrgAPIKey || args.OrgID == "" {
		return nil
	}
	keyName := defaultIdentifier(args.OrgAPIKeyName, info.CanonicalName)
	k, err := neonprov.NewOrgApiKey(ctx, name+"-org-key", &neonprov.OrgApiKeyArgs{
		OrgId: pulumi.String(args.OrgID),
		Name:  pulumi.String(keyName),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.OrgAPIKey = k
	return nil
}

func buildNeonAccountLink(info core.AccountInfo, res *Account) core.LinkDescriptor {
	exports := map[string]pulumi.Input{
		"neonOrgId":           res.OrgID,
		"neonDefaultRegionId": res.DefaultRegionID,
	}
	if res.OrgAPIKey != nil {
		exports["neonOrgApiKey"] = pulumi.ToSecret(res.OrgAPIKey.Key).(pulumi.StringOutput)
	}
	return core.LinkDescriptor{
		Name: info.Name,
		Kind: info.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"neonOrgId":           res.OrgID,
			"neonDefaultRegionId": res.DefaultRegionID,
		},
		StackExports: exports,
	}
}

func registerNeonAccountOutputs(ctx *pulumi.Context, res *Account) error {
	outs := pulumi.Map{
		"neonOrgId":           res.OrgID,
		"neonDefaultRegionId": res.DefaultRegionID,
	}
	if res.OrgAPIKey != nil {
		outs["neonOrgApiKey"] = pulumi.ToSecret(res.OrgAPIKey.Key)
	}
	return ctx.RegisterResourceOutputs(res, outs)
}
