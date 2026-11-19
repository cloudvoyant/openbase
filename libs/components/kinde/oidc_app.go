package kinde

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatelessResource              = (*OidcAppResource)(nil)
	_ core.ScopedSpecOf[*OidcAppResource] = OidcApp{}
)

// ErrOidcSourceRequired reports missing KindeProject / ProjectStackRef / explicit credentials on OidcApp.
var ErrOidcSourceRequired = errors.New("kinde: kindeProject, projectStackRef, or explicit tenant outputs are required")

// OidcApp declares per-environment Kinde OIDC credentials and redirect URLs.
type OidcApp struct {
	Name              string
	Project           string
	Repo              string
	Environment       string
	Kind              string
	Links             []core.Resource
	KindeProject      *ProjectResource
	ProjectStackRef   string
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

// OidcAppResource resolves tier-appropriate Kinde tenant credentials and redirect URL outputs.
type OidcAppResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	Tenant                string
	IssuerURL             pulumi.StringOutput
	ClientID              pulumi.StringOutput
	ClientSecret          pulumi.StringOutput
	SiteURL               pulumi.StringOutput
	RedirectURL           pulumi.StringOutput
	PostLoginRedirectURL  pulumi.StringOutput
	PostLogoutRedirectURL pulumi.StringOutput
}

// Register provisions a per-environment Kinde OidcApp component satisfying core.StatelessResource.
func (o OidcApp) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*OidcAppResource, error) {
	if !hasOidcSource(o) {
		return nil, ErrOidcSourceRequired
	}
	res := &OidcAppResource{}
	meta, err := core.InitStateless(ctx, "openbase:kinde:OidcApp", "kinde-oidc-app", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        o.Name,
		Project:     o.Project,
		Repo:        o.Repo,
		Environment: o.Environment,
		Kind:        o.Kind,
		Links:       o.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := populateOidcOutputs(ctx, o.Name, meta, o, res); err != nil {
		return nil, err
	}
	res.SetLink(buildOidcLink(meta, res))
	if err := registerOidcOutputs(ctx, meta, res); err != nil {
		return nil, err
	}
	return res, nil
}

// RegisterInScope adapts kinde.OidcApp to openbase.Project and openbase.Environment.
func (o OidcApp) RegisterInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (core.Resource, error) {
	return o.RegisterTypedInScope(ctx, scope, opts...)
}

// RegisterTypedInScope registers kinde.OidcApp with inherited Scope metadata and ProjectStackRef.
func (o OidcApp) RegisterTypedInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (*OidcAppResource, error) {
	if o.Project == "" {
		o.Project = scope.Project
	}
	if o.Repo == "" {
		o.Repo = scope.Repo
	}
	if o.Environment == "" {
		o.Environment = string(scope.Env)
	}
	if o.KindeProject == nil && scope.ProjectRef != nil {
		if kp, ok := scope.ProjectRef.ResourceHandle("kinde-project").(*ProjectResource); ok {
			o.KindeProject = kp
		}
	}
	if o.ProjectStackRef == "" && o.KindeProject == nil && o.IssuerURL == nil {
		o.ProjectStackRef = scope.ProjectStackName()
	}
	return o.Register(ctx, opts...)
}

// Private helpers ------------------------------------------------------------

func hasOidcSource(spec OidcApp) bool {
	if spec.KindeProject != nil || spec.ProjectStackRef != "" {
		return true
	}
	return spec.IssuerURL != nil && spec.ClientID != nil && spec.ClientSecret != nil
}

func populateOidcOutputs(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec OidcApp,
	res *OidcAppResource,
) error {
	useProd := meta.Environment == core.EnvStage || meta.Environment == core.EnvProd
	res.Tenant = "non-prod"
	if useProd {
		res.Tenant = "prod"
	}
	var projSR *pulumi.StackReference
	if spec.ProjectStackRef != "" && spec.KindeProject == nil {
		sr, err := pulumi.NewStackReference(ctx, name+"-project-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(spec.ProjectStackRef)})
		if err != nil {
			return err
		}
		projSR = sr
	}
	issuer, clientID, clientSecret := resolveTenantCredOutputs(useProd, spec, projSR)
	siteURL := resolveSiteURL(meta, spec, projSR)
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
	return nil
}

func resolveTenantCredOutputs(
	useProd bool,
	spec OidcApp,
	sr *pulumi.StackReference,
) (pulumi.StringOutput, pulumi.StringOutput, pulumi.StringOutput) {
	if spec.IssuerURL != nil && spec.ClientID != nil && spec.ClientSecret != nil {
		return spec.IssuerURL.ToStringOutput(), spec.ClientID.ToStringOutput(), spec.ClientSecret.ToStringOutput()
	}
	if spec.KindeProject != nil {
		if useProd {
			return spec.KindeProject.ProdIssuerURL, spec.KindeProject.ProdClientID, spec.KindeProject.ProdClientSecret
		}
		return spec.KindeProject.NonProdIssuerURL, spec.KindeProject.NonProdClientID, spec.KindeProject.NonProdClientSecret
	}
	prefix := "nonProd"
	if useProd {
		prefix = "prod"
	}
	return sr.GetStringOutput(pulumi.String(prefix + "IssuerUrl")),
		sr.GetStringOutput(pulumi.String(prefix + "ClientId")),
		pulumi.ToSecret(sr.GetStringOutput(pulumi.String(prefix + "ClientSecret"))).(pulumi.StringOutput)
}

func resolveSiteURL(meta core.ResourceMeta, spec OidcApp, sr *pulumi.StackReference) pulumi.StringOutput {
	if spec.SiteURL != nil {
		return spec.SiteURL.ToStringOutput()
	}
	vProjOut, teamOut, baseOut := resolveVercelHostParts(meta.Project, spec, sr)
	return pulumi.All(vProjOut, teamOut, baseOut).ApplyT(func(vs []any) string {
		return computeDefaultSiteOrigin(meta.Project, meta.Environment, vs[0].(string), vs[1].(string), vs[2].(string), spec)
	}).(pulumi.StringOutput)
}

func resolveVercelHostParts(
	project string,
	spec OidcApp,
	sr *pulumi.StackReference,
) (pulumi.StringOutput, pulumi.StringOutput, pulumi.StringOutput) {
	vProj := pulumi.String(defaultKindeStr(spec.VercelProjectName, project)).ToStringOutput()
	team := pulumi.String(defaultKindeStr(spec.VercelTeamSlug, "local")).ToStringOutput()
	base := pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput()
	if sr == nil {
		return vProj, team, base
	}
	if spec.VercelProjectName == "" {
		vProj = optionalKindeStackOutput(sr, "vercelProjectName", project)
	}
	if spec.VercelTeamSlug == "" {
		team = optionalKindeStackOutput(sr, "teamSlug", "local")
	}
	if spec.BaseDomain == "" {
		base = optionalKindeStackOutput(sr, "baseDomain", "")
	}
	return vProj, team, base
}

func optionalKindeStackOutput(sr *pulumi.StackReference, key, fallback string) pulumi.StringOutput {
	return sr.GetOutput(pulumi.String(key)).ApplyT(func(v any) string {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
		return fallback
	}).(pulumi.StringOutput)
}

func computeDefaultSiteOrigin(project string, env core.Environment, vProj, team, baseDomain string, spec OidcApp) string {
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

func explicitKindeEnvDomain(env core.Environment, spec OidcApp) string {
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

func buildOidcLink(meta core.ResourceMeta, res *OidcAppResource) core.LinkDescriptor {
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"tenant":      pulumi.String(res.Tenant).ToStringOutput(),
			"issuerUrl":   res.IssuerURL,
			"clientId":    res.ClientID,
			"siteUrl":     res.SiteURL,
			"redirectUrl": res.RedirectURL,
		},
		StackExports: map[string]pulumi.Input{
			"kindeIssuerUrl":   res.IssuerURL,
			"kindeRedirectUrl": res.RedirectURL,
		},
	}
}

func registerOidcOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *OidcAppResource) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName":         pulumi.String(meta.CanonicalName),
		"tenant":                pulumi.String(res.Tenant),
		"issuerUrl":             res.IssuerURL,
		"clientId":              res.ClientID,
		"clientSecret":          res.ClientSecret,
		"siteUrl":               res.SiteURL,
		"redirectUrl":           res.RedirectURL,
		"postLoginRedirectUrl":  res.PostLoginRedirectURL,
		"postLogoutRedirectUrl": res.PostLogoutRedirectURL,
	})
}
