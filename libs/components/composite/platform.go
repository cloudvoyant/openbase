package composite

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

// Public API -----------------------------------------------------------------

var _ core.PlatformResource = (*PlatformResource)(nil)

// Platform declares organization-wide GCP, Vercel, Neon, and Kinde foundations provisioned once per organization.
type Platform struct {
	Name             string
	Org              string
	GCP              *gcp.Platform
	Vercel           *vercel.Platform
	Neon             *neon.Platform
	Kinde            *kinde.Platform
	SkipStackExports bool
}

// PlatformResource holds provisioned organization-level provider foundations exported for per-repo Project stacks.
type PlatformResource struct {
	pulumi.ResourceState
	core.PlatformMeta

	GCP    *gcp.PlatformResource
	Vercel *vercel.PlatformResource
	Neon   *neon.PlatformResource
	Kinde  *kinde.PlatformResource
}

// Register provisions non-nil organization-wide provider platforms on the central platform stack.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	p.Name = defaultCompStr(p.Name, "platform")
	orgScope := core.OrgScope(p.Org, ctx.Organization())
	res := &PlatformResource{}
	info, err := core.InitPlatform(ctx, "openbase:composite:Platform", "composite-platform", res, &res.PlatformMeta, core.PlatformSpec{
		Name: p.Name,
		Org:  orgScope,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionOrgPlatform(ctx, orgScope, p, res); err != nil {
		return nil, err
	}
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(info.CanonicalName),
	})
}

// Private helpers ------------------------------------------------------------

func provisionOrgPlatform(ctx *pulumi.Context, orgScope string, spec Platform, res *PlatformResource) error {
	if err := registerOptionalGCPPlatform(ctx, orgScope, spec.GCP, res); err != nil {
		return err
	}
	if err := registerOptionalVercelPlatform(ctx, orgScope, spec.Vercel, res); err != nil {
		return err
	}
	if err := registerOptionalNeonPlatform(ctx, orgScope, spec.Neon, res); err != nil {
		return err
	}
	if err := registerOptionalKindePlatform(ctx, orgScope, spec.Kinde, res); err != nil {
		return err
	}
	exportOrgPlatformOutputs(ctx, spec.SkipStackExports, res)
	return nil
}

func registerOptionalGCPPlatform(ctx *pulumi.Context, orgScope string, cfg *gcp.Platform, res *PlatformResource) error {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	cp.Org = defaultCompStr(cp.Org, orgScope)
	out, err := cp.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.GCP = out
	return nil
}

func registerOptionalVercelPlatform(ctx *pulumi.Context, orgScope string, cfg *vercel.Platform, res *PlatformResource) error {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	cp.Org = defaultCompStr(cp.Org, orgScope)
	out, err := cp.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Vercel = out
	return nil
}

func registerOptionalNeonPlatform(ctx *pulumi.Context, orgScope string, cfg *neon.Platform, res *PlatformResource) error {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	cp.Org = defaultCompStr(cp.Org, orgScope)
	out, err := cp.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Neon = out
	return nil
}

func registerOptionalKindePlatform(ctx *pulumi.Context, orgScope string, cfg *kinde.Platform, res *PlatformResource) error {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	cp.Org = defaultCompStr(cp.Org, orgScope)
	out, err := cp.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.Kinde = out
	return nil
}

func exportOrgPlatformOutputs(ctx *pulumi.Context, skip bool, res *PlatformResource) {
	if skip {
		return
	}
	exportGCPPlatformOutputs(ctx, res.GCP)
	exportVercelPlatformOutputs(ctx, res.Vercel)
	exportNeonPlatformOutputs(ctx, res.Neon)
	exportKindePlatformOutputs(ctx, res.Kinde)
}

func exportGCPPlatformOutputs(ctx *pulumi.Context, g *gcp.PlatformResource) {
	if g == nil {
		return
	}
	ctx.Export("location", g.Location)
	ctx.Export("gcpBaseDomain", g.BaseDomain)
	ctx.Export("workloadIdentityPoolName", g.WorkloadIdentityPoolName)
	ctx.Export("vercelOidcProviderName", g.VercelOIDCProviderName)
	ctx.Export("keyRingId", g.KeyRingID)
	if g.LoadBalancer != nil {
		ctx.Export("loadBalancerIp", g.LoadBalancer.IPAddress)
		ctx.Export("dnsAuthRecordName", g.LoadBalancer.DnsAuthRecordName)
		ctx.Export("dnsAuthRecordData", g.LoadBalancer.DnsAuthRecordData)
	}
}

func exportVercelPlatformOutputs(ctx *pulumi.Context, v *vercel.PlatformResource) {
	if v == nil {
		return
	}
	ctx.Export("teamId", v.TeamID)
	ctx.Export("teamSlug", v.TeamSlug)
	ctx.Export("vercelBaseDomain", v.BaseDomain)
}

func exportNeonPlatformOutputs(ctx *pulumi.Context, n *neon.PlatformResource) {
	if n == nil {
		return
	}
	ctx.Export("neonOrgId", n.OrgID)
	ctx.Export("neonRegionId", n.RegionID)
	ctx.Export("neonPostgresVersion", pulumi.Int(n.PostgresVersion))
	ctx.Export("neonHistoryRetentionSeconds", pulumi.Int(n.HistoryRetentionSeconds))
}

func exportKindePlatformOutputs(ctx *pulumi.Context, k *kinde.PlatformResource) {
	if k == nil {
		return
	}
	ctx.Export("devIssuerUrl", k.DevIssuerURL)
	ctx.Export("prodIssuerUrl", k.ProdIssuerURL)
	ctx.Export("emailPasswordConn", pulumi.String(k.EmailPasswordConn))
}
