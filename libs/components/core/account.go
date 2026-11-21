package core

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Public API -----------------------------------------------------------------

const (
	// TierAccount marks organization-level cloud and SaaS account foundations (<provider>.Account).
	TierAccount = "account"
	// TierResource marks project-shared and per-environment resources (StatelessResource and StatefulResource).
	TierResource = "resource"
)

// AccountInfo captures validated metadata for an organization-level AccountResource.
type AccountInfo struct {
	Name          string
	CanonicalName string
	Org           string
	Kind          string
	Tags          map[string]string
}

// AccountSpec holds organization-level inputs for registering an AccountResource.
type AccountSpec struct {
	Name string
	Org  string
	Kind string
}

// AccountResource marks organization-level cloud or SaaS account handles provisioned in infra/platform.
type AccountResource interface {
	pulumi.Resource
	OpenBaseAccount() AccountInfo
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

// OIDCProviderSource is implemented by account resources (such as *vercel.Account or OIDCIssuer) that publish an OIDC trust spec.
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

// AccountMeta stores AccountInfo and Links on structs that directly embed pulumi.ResourceState.
type AccountMeta struct {
	info AccountInfo
	link LinkDescriptor
}

// OpenBaseAccount returns the validated organization metadata for the account component.
func (b *AccountMeta) OpenBaseAccount() AccountInfo { return b.info }

// Link returns the LinkDescriptor exported by the account component.
func (b *AccountMeta) Link() LinkDescriptor { return b.link }

// SetLink sets the LinkDescriptor exported by the account component without stack exports.
func (b *AccountMeta) SetLink(desc LinkDescriptor) { b.link = desc }

// PublishLink sets the LinkDescriptor and records stack exports and openbaseManifest entries on ctx.
func (b *AccountMeta) PublishLink(ctx *pulumi.Context, desc LinkDescriptor) {
	b.link = desc
	RecordStackExports(ctx, desc)
}

// InitAccount validates spec and registers an organization-level AccountResource component.
func InitAccount(
	ctx *pulumi.Context,
	typeToken, defaultKind string,
	self pulumi.ComponentResource,
	holder *AccountMeta,
	spec AccountSpec,
	opts ...pulumi.ResourceOption,
) (AccountInfo, error) {
	org := OrgScope(spec.Org, ctx.Organization())
	if err := ValidateIdentifier("name", spec.Name); err != nil {
		return AccountInfo{}, err
	}
	kind := spec.Kind
	if kind == "" {
		kind = defaultKind
	}
	info := AccountInfo{
		Name:          spec.Name,
		CanonicalName: BoundedName(fmt.Sprintf("%s-%s", org, spec.Name), 63),
		Org:           org,
		Kind:          kind,
		Tags: map[string]string{
			TagTier: TierAccount,
			TagOrg:  org,
		},
	}
	holder.info = info
	if err := ctx.RegisterComponentResource(typeToken, spec.Name, self, opts...); err != nil {
		return AccountInfo{}, err
	}
	return info, nil
}

// GCPAccountLabels builds GCP-valid labels for an organization-level AccountResource.
func GCPAccountLabels(info AccountInfo, extra map[string]string) pulumi.StringMap {
	labels := pulumi.StringMap{
		"openbase-tier": pulumi.String(TierAccount),
		"openbase-org":  pulumi.String(sanitizeGCPLabelValue(info.Org)),
	}
	appendExtraGCPLabels(labels, extra)
	return labels
}

// CustomAccountArgs configures an escape-hatch organization AccountResource component.
type CustomAccountArgs struct {
	Org        string
	Kind       string
	Attributes map[string]pulumi.StringInput
}

// CustomAccount is an escape-hatch organization AccountResource handle.
type CustomAccount struct {
	pulumi.ResourceState
	AccountMeta
}

// NewCustomAccount registers an escape-hatch AccountResource with canonical org tags and attributes.
func NewCustomAccount(ctx *pulumi.Context, name string, args *CustomAccountArgs, opts ...pulumi.ResourceOption) (*CustomAccount, error) {
	if args == nil {
		args = &CustomAccountArgs{}
	}
	res := &CustomAccount{}
	info, err := InitAccount(ctx, "openbase:core:AccountResource", "custom-account", res, &res.AccountMeta, AccountSpec{
		Name: name,
		Org:  args.Org,
		Kind: args.Kind,
	}, opts...)
	if err != nil {
		return nil, err
	}
	attrs := map[string]pulumi.StringOutput{}
	for k, v := range args.Attributes {
		attrs[k] = v.ToStringOutput()
	}
	res.PublishLink(ctx, LinkDescriptor{
		Name:       info.Name,
		Kind:       info.Kind,
		Attributes: attrs,
	})
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{"canonicalName": pulumi.String(info.CanonicalName)})
}
