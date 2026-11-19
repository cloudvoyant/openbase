// Package neon provides OpenBase Pulumi components for Neon serverless PostgreSQL across Platform, Project, and Database tiers.
package neon

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-neon-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.PlatformResource = (*PlatformResource)(nil)
	_ core.PlatformSpecItem = Platform{}
)

// Platform declares account/organization-level Neon settings and optional org API keys provisioned once per organization.
type Platform struct {
	Name            string
	Org             string
	OrgID           string
	RegionID        string
	APIKey          pulumi.StringInput
	APIBaseURL      string
	CreateOrgAPIKey bool
	OrgAPIKeyName   string
}

// PlatformResource holds account-level Neon defaults exported for per-repo neon.Project stacks.
type PlatformResource struct {
	pulumi.ResourceState
	core.PlatformMeta

	OrgAPIKey  *neonprov.OrgApiKey
	OrgID      pulumi.StringOutput
	RegionID   pulumi.StringOutput
	apiKey     pulumi.StringInput
	apiBaseURL string
}

// Register provisions org-level Neon account configuration on a central platform stack.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	p.Name = defaultIdentifier(p.Name, "neon-platform")
	res := &PlatformResource{}
	info, err := core.InitPlatform(ctx, "openbase:neon:Platform", "neon-platform", res, &res.PlatformMeta, core.PlatformSpec{
		Name: p.Name,
		Org:  p.Org,
	}, opts...)
	if err != nil {
		return nil, err
	}
	populateNeonPlatformDefaults(p, res)
	if err := createOptionalNeonOrgKey(ctx, p.Name, info, p, res); err != nil {
		return nil, err
	}
	res.SetLink(buildNeonPlatformLink(info, res))
	return res, registerNeonPlatformOutputs(ctx, res)
}

// RegisterPlatformInScope adapts neon.Platform to openbase.Platform.
func (p Platform) RegisterPlatformInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (core.PlatformResource, error) {
	if p.Org == "" {
		p.Org = scope.Org
	}
	return p.Register(ctx, opts...)
}

// Private helpers ------------------------------------------------------------

func populateNeonPlatformDefaults(spec Platform, res *PlatformResource) {
	region := defaultIdentifier(spec.RegionID, "aws-us-east-1")
	res.OrgID = pulumi.String(spec.OrgID).ToStringOutput()
	res.RegionID = pulumi.String(region).ToStringOutput()
	res.apiKey = spec.APIKey
	res.apiBaseURL = spec.APIBaseURL
}

func createOptionalNeonOrgKey(
	ctx *pulumi.Context,
	name string,
	info core.PlatformInfo,
	spec Platform,
	res *PlatformResource,
) error {
	if !spec.CreateOrgAPIKey || spec.OrgID == "" {
		return nil
	}
	keyName := defaultIdentifier(spec.OrgAPIKeyName, info.CanonicalName)
	k, err := neonprov.NewOrgApiKey(ctx, name+"-org-key", &neonprov.OrgApiKeyArgs{
		OrgId: pulumi.String(spec.OrgID),
		Name:  pulumi.String(keyName),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.OrgAPIKey = k
	return nil
}

func buildNeonPlatformLink(info core.PlatformInfo, res *PlatformResource) core.LinkDescriptor {
	exports := map[string]pulumi.Input{
		"neonOrgId":    res.OrgID,
		"neonRegionId": res.RegionID,
	}
	if res.OrgAPIKey != nil {
		exports["neonOrgApiKey"] = pulumi.ToSecret(res.OrgAPIKey.Key).(pulumi.StringOutput)
	}
	return core.LinkDescriptor{
		Name: info.Name,
		Kind: info.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"neonOrgId":    res.OrgID,
			"neonRegionId": res.RegionID,
		},
		StackExports: exports,
	}
}

func registerNeonPlatformOutputs(ctx *pulumi.Context, res *PlatformResource) error {
	outs := pulumi.Map{
		"neonOrgId":    res.OrgID,
		"neonRegionId": res.RegionID,
	}
	if res.OrgAPIKey != nil {
		outs["neonOrgApiKey"] = pulumi.ToSecret(res.OrgAPIKey.Key)
	}
	return ctx.RegisterResourceOutputs(res, outs)
}
