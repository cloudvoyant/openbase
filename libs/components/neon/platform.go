// Package neon provides OpenBase Pulumi components for Neon serverless PostgreSQL across Platform, Project, and Database tiers.
package neon

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-neon-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.PlatformResource = (*Platform)(nil)

// PlatformArgs configures account/organization-level Neon settings and optional org API keys provisioned once per organization.
type PlatformArgs struct {
	Org             string
	OrgID           string
	DefaultRegionID string
	APIKey          pulumi.StringInput
	APIBaseURL      string
	CreateOrgAPIKey bool
	OrgAPIKeyName   string
}

// Platform holds account-level Neon defaults exported for per-repo neon.Project stacks.
type Platform struct {
	pulumi.ResourceState
	core.PlatformMeta

	OrgAPIKey       *neonprov.OrgApiKey
	OrgID           pulumi.StringOutput
	DefaultRegionID pulumi.StringOutput
	apiKey          pulumi.StringInput
	apiBaseURL      string
}

// NewPlatform provisions org-level Neon account configuration on a central platform stack.
func NewPlatform(ctx *pulumi.Context, name string, args *PlatformArgs, opts ...pulumi.ResourceOption) (*Platform, error) {
	if args == nil {
		args = &PlatformArgs{}
	}
	name = defaultIdentifier(name, "neon-platform")
	res := &Platform{}
	info, err := core.InitPlatform(ctx, "openbase:neon:Platform", "neon-platform", res, &res.PlatformMeta, core.PlatformSpec{
		Name: name,
		Org:  args.Org,
	}, opts...)
	if err != nil {
		return nil, err
	}
	populateNeonPlatformDefaults(args, res)
	if err := createOptionalNeonOrgKey(ctx, name, info, args, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, buildNeonPlatformLink(info, res))
	return res, registerNeonPlatformOutputs(ctx, res)
}

// Private helpers ------------------------------------------------------------

func populateNeonPlatformDefaults(args *PlatformArgs, res *Platform) {
	region := defaultIdentifier(args.DefaultRegionID, "aws-us-east-1")
	res.OrgID = pulumi.String(args.OrgID).ToStringOutput()
	res.DefaultRegionID = pulumi.String(region).ToStringOutput()
	res.apiKey = args.APIKey
	res.apiBaseURL = args.APIBaseURL
}

func createOptionalNeonOrgKey(
	ctx *pulumi.Context,
	name string,
	info core.PlatformInfo,
	args *PlatformArgs,
	res *Platform,
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

func buildNeonPlatformLink(info core.PlatformInfo, res *Platform) core.LinkDescriptor {
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

func registerNeonPlatformOutputs(ctx *pulumi.Context, res *Platform) error {
	outs := pulumi.Map{
		"neonOrgId":           res.OrgID,
		"neonDefaultRegionId": res.DefaultRegionID,
	}
	if res.OrgAPIKey != nil {
		outs["neonOrgApiKey"] = pulumi.ToSecret(res.OrgAPIKey.Key)
	}
	return ctx.RegisterResourceOutputs(res, outs)
}
