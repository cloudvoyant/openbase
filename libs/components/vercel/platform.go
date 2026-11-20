// Package vercel provides OpenBase Pulumi components for Vercel across Platform, Project, WebApp, and BlobStore tiers.
package vercel

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.PlatformResource                      = (*PlatformResource)(nil)
	_ core.PlatformSpecItemOf[*PlatformResource] = Platform{}
	_ core.OIDCProviderSource                    = Platform{}
	_ core.OIDCProviderSource                    = (*PlatformResource)(nil)
)

// Platform declares account/team-level Vercel settings provisioned once per organization.
type Platform struct {
	Name           string
	Org            string
	TeamID         string
	TeamSlug       string
	OIDCIssuerMode string
	BaseDomain     string
}

// PlatformResource holds team-level Vercel configuration exported for downstream vercel.Project stacks and OIDC federation.
type PlatformResource struct {
	pulumi.ResourceState
	core.PlatformMeta

	TeamID     pulumi.StringOutput
	TeamSlug   pulumi.StringOutput
	BaseDomain pulumi.StringOutput
	oidcSpec   core.OIDCIssuer
}

// Register exports team-level Vercel settings for consumption by vercel.Project stacks and linked cloud IAM pools.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	p.Name = defaultVercelStr(p.Name, "vercel-platform")
	orgScope := core.OrgScope(p.Org, p.TeamSlug, ctx.Organization())
	issuer, _ := p.OIDCIssuerSpec()
	res := &PlatformResource{
		TeamID:     pulumi.String(p.TeamID).ToStringOutput(),
		TeamSlug:   pulumi.String(p.TeamSlug).ToStringOutput(),
		BaseDomain: pulumi.String(core.NormalizeDomain(p.BaseDomain)).ToStringOutput(),
		oidcSpec:   issuer,
	}
	info, err := core.InitPlatform(ctx, "openbase:vercel:Platform", "vercel-platform", res, &res.PlatformMeta, core.PlatformSpec{
		Name: p.Name,
		Org:  orgScope,
	}, opts...)
	if err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
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

// RegisterPlatformInScope adapts vercel.Platform to openbase.Platform.
func (p Platform) RegisterPlatformInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (core.PlatformResource, error) {
	return p.RegisterTypedPlatformInScope(ctx, scope, opts...)
}

// RegisterTypedPlatformInScope registers vercel.Platform with inherited Org defaults and explicit ResourceOptions.
func (p Platform) RegisterTypedPlatformInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	if p.Org == "" {
		p.Org = scope.Org
	}
	return p.Register(ctx, opts...)
}

// OIDCIssuerSpec publishes Vercel's team or global OIDC issuer descriptor for cross-provider federation.
func (p Platform) OIDCIssuerSpec() (core.OIDCIssuer, bool) {
	return OIDCIssuer(p.TeamSlug, p.OIDCIssuerMode)
}

// OIDCIssuerSpec returns the provisioned Vercel team OIDC issuer descriptor.
func (r *PlatformResource) OIDCIssuerSpec() (core.OIDCIssuer, bool) {
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
