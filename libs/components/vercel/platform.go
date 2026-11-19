// Package vercel provides OpenBase Pulumi components for Vercel across Platform, Project, WebApp, and BlobStore tiers.
package vercel

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*PlatformResource)(nil)

// Platform declares organization/team-level Vercel settings provisioned once per organization.
type Platform struct {
	Name       string
	Org        string
	TeamID     string
	TeamSlug   string
	BaseDomain string
}

// PlatformResource holds team-level Vercel configuration exported for downstream vercel.Project stacks.
type PlatformResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	TeamID     pulumi.StringOutput
	TeamSlug   pulumi.StringOutput
	BaseDomain pulumi.StringOutput
}

// Register exports team-level Vercel settings for consumption by vercel.Project stacks.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	p.Name = defaultVercelStr(p.Name, "vercel-platform")
	orgScope := core.OrgScope(p.Org, p.TeamSlug, ctx.Organization())
	res := &PlatformResource{
		TeamID:     pulumi.String(p.TeamID).ToStringOutput(),
		TeamSlug:   pulumi.String(p.TeamSlug).ToStringOutput(),
		BaseDomain: pulumi.String(core.NormalizeDomain(p.BaseDomain)).ToStringOutput(),
	}
	meta, err := core.InitStateless(ctx, "openbase:vercel:Platform", "vercel-platform", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        p.Name,
		Project:     orgScope,
		Environment: string(core.EnvShared),
	}, opts...)
	if err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env: map[string]pulumi.StringOutput{
			"VERCEL_TEAM_ID":     res.TeamID,
			"VERCEL_TEAM_SLUG":   res.TeamSlug,
			"VERCEL_BASE_DOMAIN": res.BaseDomain,
		},
	})
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"teamId":           res.TeamID,
		"teamSlug":         res.TeamSlug,
		"baseDomain":       res.BaseDomain,
		"vercelBaseDomain": res.BaseDomain,
	})
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
