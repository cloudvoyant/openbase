// Package neon provides OpenBase Pulumi components for Neon serverless PostgreSQL across Platform, Project, and Database tiers.
package neon

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/internal/providers/neon"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*PlatformResource)(nil)

// Platform declares organization-level Neon settings and optional org API keys provisioned once per organization.
type Platform struct {
	Name                    string
	Project                 string
	Repo                    string
	Environment             string
	OrgID                   string
	RegionID                string
	PostgresVersion         int
	HistoryRetentionSeconds int
	CreateOrgAPIKey         bool
	OrgAPIKeyName           string
}

// PlatformResource holds organization-level Neon defaults exported for per-repo neon.Project stacks.
type PlatformResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	OrgAPIKey               *neonprov.OrgApiKey
	OrgID                   pulumi.StringOutput
	RegionID                pulumi.StringOutput
	PostgresVersion         int
	HistoryRetentionSeconds int
}

// Register provisions org-level Neon configuration on a central platform stack.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	p.Name = defaultIdentifier(p.Name, "neon-platform")
	p.Project = defaultIdentifier(p.Project, "platform")
	env := defaultIdentifier(p.Environment, string(core.EnvShared))
	res := &PlatformResource{}
	meta, err := core.InitStateless(ctx, "openbase:neon:Platform", "neon-platform", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        p.Name,
		Project:     p.Project,
		Repo:        p.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	populateNeonPlatformDefaults(p, res)
	if err := createOptionalNeonOrgKey(ctx, p.Name, meta, p, res); err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env: map[string]pulumi.StringOutput{
			"NEON_ORG_ID":    res.OrgID,
			"NEON_REGION_ID": res.RegionID,
		},
	})
	return res, registerNeonPlatformOutputs(ctx, res)
}

// Private helpers ------------------------------------------------------------

func populateNeonPlatformDefaults(spec Platform, res *PlatformResource) {
	orgID := defaultIdentifier(spec.OrgID, os.Getenv("NEON_ORG_ID"))
	region := defaultIdentifier(spec.RegionID, "aws-us-east-1")
	pgVer := spec.PostgresVersion
	if pgVer == 0 {
		pgVer = 17
	}
	retention := spec.HistoryRetentionSeconds
	if retention == 0 {
		retention = 86400
	}
	res.OrgID = pulumi.String(orgID).ToStringOutput()
	res.RegionID = pulumi.String(region).ToStringOutput()
	res.PostgresVersion = pgVer
	res.HistoryRetentionSeconds = retention
}

func createOptionalNeonOrgKey(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Platform,
	res *PlatformResource,
) error {
	orgID := defaultIdentifier(spec.OrgID, os.Getenv("NEON_ORG_ID"))
	if !spec.CreateOrgAPIKey || orgID == "" {
		return nil
	}
	keyName := defaultIdentifier(spec.OrgAPIKeyName, meta.CanonicalName)
	k, err := neonprov.NewOrgApiKey(ctx, name+"-org-key", &neonprov.OrgApiKeyArgs{
		OrgId: pulumi.String(orgID),
		Name:  pulumi.String(keyName),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.OrgAPIKey = k
	return nil
}

func registerNeonPlatformOutputs(ctx *pulumi.Context, res *PlatformResource) error {
	outs := pulumi.Map{
		"neonOrgId":                   res.OrgID,
		"neonRegionId":                res.RegionID,
		"neonPostgresVersion":         pulumi.Int(res.PostgresVersion),
		"neonHistoryRetentionSeconds": pulumi.Int(res.HistoryRetentionSeconds),
	}
	if res.OrgAPIKey != nil {
		outs["neonOrgApiKey"] = pulumi.ToSecret(res.OrgAPIKey.Key)
	}
	return ctx.RegisterResourceOutputs(res, outs)
}
