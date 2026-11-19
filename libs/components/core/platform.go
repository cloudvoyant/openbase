package core

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Public API -----------------------------------------------------------------

const (
	// TierPlatform marks organization-wide foundations (<provider>.Platform and openbase.Platform).
	TierPlatform = "platform"
	// TierResource marks project-shared and per-environment resources (StatelessResource and StatefulResource).
	TierResource = "resource"
)

// PlatformInfo captures validated metadata for an organization-level PlatformResource.
type PlatformInfo struct {
	Name          string
	CanonicalName string
	Org           string
	Kind          string
	Tags          map[string]string
}

// PlatformSpec holds organization-level inputs for registering a PlatformResource.
type PlatformSpec struct {
	Name string
	Org  string
	Kind string
}

// PlatformResource marks organization-wide foundation handles provisioned once per org.
type PlatformResource interface {
	pulumi.Resource
	OpenBasePlatform() PlatformInfo
	Link() LinkDescriptor
}

// OIDCIssuer describes an external OIDC identity issuer that can be federated into a cloud account IAM pool.
type OIDCIssuer struct {
	ID                 string
	DisplayName        string
	IssuerURI          string
	AllowedAudiences   []string
	SubjectExpr        string
	AttributeCondition string
	Claims             map[string]string
	PrincipalClaim     string
}

// OIDCProviderSource is implemented by account providers (such as vercel.Platform or OIDCIssuer) that publish an OIDC trust spec.
type OIDCProviderSource interface {
	OIDCIssuerSpec() (OIDCIssuer, bool)
}

// OIDCIssuerSpec satisfies OIDCProviderSource on literal OIDCIssuer specs.
func (i OIDCIssuer) OIDCIssuerSpec() (OIDCIssuer, bool) {
	return i, i.ID != "" && i.IssuerURI != ""
}

// GitHubActionsOIDC returns a generic OIDCIssuer trusting GitHub Actions workflows in githubOrg.
func GitHubActionsOIDC(githubOrg string) OIDCIssuer {
	if githubOrg == "" {
		return OIDCIssuer{}
	}
	return OIDCIssuer{
		ID:                 "github",
		DisplayName:        "GitHub Actions OIDC",
		IssuerURI:          "https://token.actions.githubusercontent.com",
		SubjectExpr:        "assertion.sub",
		AttributeCondition: fmt.Sprintf("assertion.repository_owner == '%s'", githubOrg),
		Claims: map[string]string{
			"actor":      "assertion.actor",
			"repository": "assertion.repository",
			"ref":        "assertion.ref",
		},
		PrincipalClaim: "repository",
	}
}

// PlatformMeta stores PlatformInfo and Links on structs that directly embed pulumi.ResourceState.
type PlatformMeta struct {
	info PlatformInfo
	link LinkDescriptor
}

// OpenBasePlatform returns the validated organization metadata for the platform component.
func (b *PlatformMeta) OpenBasePlatform() PlatformInfo { return b.info }

// Link returns the LinkDescriptor exported by the platform component.
func (b *PlatformMeta) Link() LinkDescriptor { return b.link }

// SetLink sets the LinkDescriptor exported by the platform component.
func (b *PlatformMeta) SetLink(desc LinkDescriptor) { b.link = desc }

// InitPlatform validates spec and registers an organization-level PlatformResource component.
func InitPlatform(
	ctx *pulumi.Context,
	typeToken, defaultKind string,
	self pulumi.ComponentResource,
	holder *PlatformMeta,
	spec PlatformSpec,
	opts ...pulumi.ResourceOption,
) (PlatformInfo, error) {
	org := OrgScope(spec.Org, ctx.Organization())
	if err := ValidateIdentifier("name", spec.Name); err != nil {
		return PlatformInfo{}, err
	}
	kind := spec.Kind
	if kind == "" {
		kind = defaultKind
	}
	info := PlatformInfo{
		Name:          spec.Name,
		CanonicalName: BoundedName(fmt.Sprintf("%s-%s", org, spec.Name), 63),
		Org:           org,
		Kind:          kind,
		Tags: map[string]string{
			TagTier: TierPlatform,
			TagOrg:  org,
		},
	}
	holder.info = info
	if err := ctx.RegisterComponentResource(typeToken, spec.Name, self, opts...); err != nil {
		return PlatformInfo{}, err
	}
	return info, nil
}

// GCPPlatformLabels builds GCP-valid labels for an organization-level PlatformResource.
func GCPPlatformLabels(info PlatformInfo, extra map[string]string) pulumi.StringMap {
	labels := pulumi.StringMap{
		"openbase-tier": pulumi.String(TierPlatform),
		"openbase-org":  pulumi.String(sanitizeGCPLabelValue(info.Org)),
	}
	appendExtraGCPLabels(labels, extra)
	return labels
}

// Platform declares an escape-hatch organization platform component wrapping custom Pulumi resources.
type Platform struct {
	Name       string
	Org        string
	Kind       string
	Attributes map[string]pulumi.StringInput
}

// CustomPlatformResource is the provisioned handle returned by Platform.Register.
type CustomPlatformResource struct {
	pulumi.ResourceState
	PlatformMeta
}

// Register validates and provisions an escape-hatch PlatformResource with canonical org tags and attributes.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*CustomPlatformResource, error) {
	res := &CustomPlatformResource{}
	info, err := InitPlatform(ctx, "openbase:core:PlatformResource", "custom-platform", res, &res.PlatformMeta, PlatformSpec{
		Name: p.Name,
		Org:  p.Org,
		Kind: p.Kind,
	}, opts...)
	if err != nil {
		return nil, err
	}
	attrs := map[string]pulumi.StringOutput{}
	for k, v := range p.Attributes {
		attrs[k] = v.ToStringOutput()
	}
	res.SetLink(LinkDescriptor{
		Name:       info.Name,
		Kind:       info.Kind,
		Attributes: attrs,
	})
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{"canonicalName": pulumi.String(info.CanonicalName)})
}

// RegisterPlatformInScope adapts core.Platform to openbase.Platform.
func (p Platform) RegisterPlatformInScope(ctx *pulumi.Context, scope Scope, opts ...pulumi.ResourceOption) (PlatformResource, error) {
	if p.Org == "" {
		p.Org = scope.Org
	}
	return p.Register(ctx, opts...)
}
