package kinde

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*OidcApp)(nil)

// ErrOidcSourceRequired reports missing Project or explicit credentials on OidcAppArgs.
var ErrOidcSourceRequired = errors.New("kinde: project or explicit environment outputs are required")

// OidcAppArgs configures per-environment Kinde OIDC credentials and redirect URLs.
type OidcAppArgs struct {
	ProjectName       string
	Repo              string
	Environment       string
	Kind              string
	Links             []core.Resource
	Project           ProjectInput
	IssuerURL         pulumi.StringInput
	ClientID          pulumi.StringInput
	ClientSecret      pulumi.StringInput
	SiteURL           pulumi.StringInput
	VercelProjectName string
	VercelTeamSlug    string
	BaseDomain        string
	DevDomain         string
	StageDomain       string
	ProdDomain        string
	CallbackPath      string
	PostLoginPath     string
	PostLogoutPath    string
}

// OidcApp resolves tier-appropriate Kinde environment credentials and redirect URL outputs.
type OidcApp struct {
	pulumi.ResourceState
	core.StatelessMeta

	KindeEnvironment      string
	IssuerURL             pulumi.StringOutput
	ClientID              pulumi.StringOutput
	ClientSecret          pulumi.StringOutput
	SiteURL               pulumi.StringOutput
	RedirectURL           pulumi.StringOutput
	PostLoginRedirectURL  pulumi.StringOutput
	PostLogoutRedirectURL pulumi.StringOutput
}

// NewOidcApp registers a per-environment Kinde OidcApp component satisfying core.StatelessResource.
func NewOidcApp(ctx *pulumi.Context, name string, args *OidcAppArgs, opts ...pulumi.ResourceOption) (*OidcApp, error) {
	if args == nil || !hasOidcSource(args) {
		return nil, ErrOidcSourceRequired
	}
	res := &OidcApp{}
	meta, err := core.InitStateless(ctx, "openbase:kinde:OidcApp", "kinde-oidc-app", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        name,
		Project:     args.ProjectName,
		Repo:        args.Repo,
		Environment: args.Environment,
		Kind:        args.Kind,
		Links:       args.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	populateOidcOutputs(meta, args, res)
	res.PublishLink(ctx, buildOidcLink(meta, res))
	return res, registerOidcOutputs(ctx, meta, res)
}

// Private helpers ------------------------------------------------------------

func hasOidcSource(spec *OidcAppArgs) bool {
	if spec.Project != nil {
		return true
	}
	return spec.IssuerURL != nil && spec.ClientID != nil && spec.ClientSecret != nil
}

func populateOidcOutputs(
	meta core.ResourceMeta,
	spec *OidcAppArgs,
	res *OidcApp,
) {
	useProd := meta.Environment == core.EnvStage || meta.Environment == core.EnvProd
	res.KindeEnvironment = "non-prod"
	if useProd {
		res.KindeEnvironment = "prod"
	}
	issuer, clientID, clientSecret := resolveEnvCredOutputs(useProd, spec)
	siteURL := resolveSiteURL(meta, spec)
	cb := ensureLeadingSlash(defaultKindeStr(spec.CallbackPath, "/api/auth/kinde_callback"))
	postLogin := ensureLeadingSlash(defaultKindeStr(spec.PostLoginPath, "/"))
	postLogout := ensureLeadingSlash(defaultKindeStr(spec.PostLogoutPath, "/"))

	res.IssuerURL = issuer
	res.ClientID = clientID
	res.ClientSecret = pulumi.ToSecret(clientSecret).(pulumi.StringOutput)
	res.SiteURL = siteURL
	res.RedirectURL = joinOriginPath(siteURL, cb)
	res.PostLoginRedirectURL = joinOriginPath(siteURL, postLogin)
	res.PostLogoutRedirectURL = joinOriginPath(siteURL, postLogout)
}

func resolveEnvCredOutputs(
	useProd bool,
	spec *OidcAppArgs,
) (pulumi.StringOutput, pulumi.StringOutput, pulumi.StringOutput) {
	if spec.IssuerURL != nil && spec.ClientID != nil && spec.ClientSecret != nil {
		return spec.IssuerURL.ToStringOutput(), spec.ClientID.ToStringOutput(), spec.ClientSecret.ToStringOutput()
	}
	pv := spec.Project.kindeProjectValues()
	if useProd {
		return pv.prodIssuerURL, pv.prodClientID, pv.prodClientSecret
	}
	return pv.nonProdIssuerURL, pv.nonProdClientID, pv.nonProdClientSecret
}

func resolveSiteURL(meta core.ResourceMeta, spec *OidcAppArgs) pulumi.StringOutput {
	if spec.SiteURL != nil {
		return spec.SiteURL.ToStringOutput()
	}
	vProjOut, teamOut, baseOut := resolveVercelHostParts(meta.Project, spec)
	return pulumi.All(vProjOut, teamOut, baseOut).ApplyT(func(vs []any) string {
		return computeDefaultSiteOrigin(meta.Project, meta.Environment, vs[0].(string), vs[1].(string), vs[2].(string), spec)
	}).(pulumi.StringOutput)
}

func resolveVercelHostParts(
	project string,
	spec *OidcAppArgs,
) (pulumi.StringOutput, pulumi.StringOutput, pulumi.StringOutput) {
	vProj := pulumi.String(defaultKindeStr(spec.VercelProjectName, project)).ToStringOutput()
	team := pulumi.String(defaultKindeStr(spec.VercelTeamSlug, "local")).ToStringOutput()
	base := pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput()
	if spec.Project == nil {
		return vProj, team, base
	}
	pv := spec.Project.kindeProjectValues()
	if spec.VercelProjectName == "" {
		vProj = defaultKindeOutput(pv.vercelProjectName, project)
	}
	if spec.VercelTeamSlug == "" {
		team = defaultKindeOutput(pv.vercelTeamSlug, "local")
	}
	if spec.BaseDomain == "" {
		base = defaultKindeOutput(pv.baseDomain, "")
	}
	return vProj, team, base
}

func defaultKindeOutput(out pulumi.StringOutput, fallback string) pulumi.StringOutput {
	return out.ApplyT(func(s string) string {
		if s != "" {
			return s
		}
		return fallback
	}).(pulumi.StringOutput)
}

func computeDefaultSiteOrigin(project string, env core.Environment, vProj, team, baseDomain string, spec *OidcAppArgs) string {
	if explicit := explicitKindeEnvDomain(env, spec); explicit != "" {
		return NormalizeIssuerURL(explicit)
	}
	if baseDomain != "" {
		if dom, err := core.EnvironmentDomain(project, env, "web", baseDomain); err == nil && dom != "" {
			return "https://" + dom
		}
	}
	switch {
	case env == core.EnvDev:
		return "http://localhost:5173"
	case env.IsPreview():
		return fmt.Sprintf("https://%s-%s-%s.vercel.app", vProj, core.EnvDomainLabel(env), team)
	case env == core.EnvStage:
		return fmt.Sprintf("https://%s-stage-%s.vercel.app", vProj, team)
	default:
		return fmt.Sprintf("https://%s-%s.vercel.app", vProj, team)
	}
}

func explicitKindeEnvDomain(env core.Environment, spec *OidcAppArgs) string {
	switch env {
	case core.EnvDev:
		return spec.DevDomain
	case core.EnvStage:
		return spec.StageDomain
	case core.EnvProd:
		return spec.ProdDomain
	default:
		return ""
	}
}

func joinOriginPath(origin pulumi.StringOutput, path string) pulumi.StringOutput {
	return origin.ApplyT(func(base string) string {
		return strings.TrimRight(base, "/") + path
	}).(pulumi.StringOutput)
}

func buildOidcLink(meta core.ResourceMeta, res *OidcApp) core.LinkDescriptor {
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"kindeEnvironment": pulumi.String(res.KindeEnvironment).ToStringOutput(),
			"issuerUrl":        res.IssuerURL,
			"clientId":         res.ClientID,
			"siteUrl":          res.SiteURL,
			"redirectUrl":      res.RedirectURL,
		},
		StackExports: map[string]pulumi.Input{
			"kindeIssuerUrl":   res.IssuerURL,
			"kindeRedirectUrl": res.RedirectURL,
		},
	}
}

func registerOidcOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *OidcApp) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName":         pulumi.String(meta.CanonicalName),
		"kindeEnvironment":      pulumi.String(res.KindeEnvironment),
		"issuerUrl":             res.IssuerURL,
		"clientId":              res.ClientID,
		"clientSecret":          res.ClientSecret,
		"siteUrl":               res.SiteURL,
		"redirectUrl":           res.RedirectURL,
		"postLoginRedirectUrl":  res.PostLoginRedirectURL,
		"postLogoutRedirectUrl": res.PostLogoutRedirectURL,
	})
}
