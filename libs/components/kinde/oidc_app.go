package kinde

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*OidcAppResource)(nil)

// ErrOidcSourceRequired reports missing Shared / SharedStackRef / explicit credentials on OidcApp.
var ErrOidcSourceRequired = errors.New("kinde: shared, sharedStackRef, or explicit tenant outputs are required")

// OidcApp declares per-environment Kinde OIDC credentials and redirect URLs.
type OidcApp struct {
	Name              string
	Project           string
	Repo              string
	Environment       string
	Kind              string
	Links             []core.Resource
	Env               map[string]pulumi.StringInput
	Secrets           map[string]pulumi.StringInput
	Shared            *SharedOidcAppResource
	SharedStackRef    string
	IssuerURL         pulumi.StringInput
	ClientID          pulumi.StringInput
	ClientSecret      pulumi.StringInput
	SiteURL           pulumi.StringInput
	VercelProjectName string
	VercelTeamSlug    string
	StageDomain       string
	ProdDomain        string
	CallbackPath      string
	PostLoginPath     string
	PostLogoutPath    string
}

// OidcAppResource resolves tier-appropriate Kinde tenant credentials and exports KINDE_* Links.
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
		Env:         o.Env,
		Secrets:     o.Secrets,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := populateOidcOutputs(ctx, o.Name, meta, o, res); err != nil {
		return nil, err
	}
	res.SetLink(buildOidcLink(meta, o, res))
	if err := registerOidcOutputs(ctx, meta, res); err != nil {
		return nil, err
	}
	return res, nil
}

// Private helpers ------------------------------------------------------------

func hasOidcSource(spec OidcApp) bool {
	if spec.Shared != nil || spec.SharedStackRef != "" {
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
	res.Tenant = "dev"
	if useProd {
		res.Tenant = "prod"
	}
	issuer, clientID, clientSecret, err := resolveTenantCredOutputs(ctx, name, useProd, spec)
	if err != nil {
		return err
	}
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
	return nil
}

func resolveTenantCredOutputs(
	ctx *pulumi.Context,
	name string,
	useProd bool,
	spec OidcApp,
) (pulumi.StringOutput, pulumi.StringOutput, pulumi.StringOutput, error) {
	if spec.IssuerURL != nil && spec.ClientID != nil && spec.ClientSecret != nil {
		return spec.IssuerURL.ToStringOutput(), spec.ClientID.ToStringOutput(), spec.ClientSecret.ToStringOutput(), nil
	}
	if spec.Shared != nil {
		if useProd {
			return spec.Shared.ProdIssuerURL, spec.Shared.ProdClientID, spec.Shared.ProdClientSecret, nil
		}
		return spec.Shared.DevIssuerURL, spec.Shared.DevClientID, spec.Shared.DevClientSecret, nil
	}
	sr, err := pulumi.NewStackReference(ctx, name+"-shared-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(spec.SharedStackRef)})
	if err != nil {
		return pulumi.StringOutput{}, pulumi.StringOutput{}, pulumi.StringOutput{}, err
	}
	prefix := "dev"
	if useProd {
		prefix = "prod"
	}
	return sr.GetStringOutput(pulumi.String(prefix + "IssuerUrl")),
		sr.GetStringOutput(pulumi.String(prefix + "ClientId")),
		pulumi.ToSecret(sr.GetOutput(pulumi.String(prefix + "ClientSecret"))).(pulumi.StringOutput),
		nil
}

func resolveSiteURL(meta core.ResourceMeta, spec OidcApp) pulumi.StringOutput {
	if spec.SiteURL != nil {
		return spec.SiteURL.ToStringOutput()
	}
	vProj := defaultKindeStr(spec.VercelProjectName, meta.Project)
	team := defaultKindeStr(spec.VercelTeamSlug, "local")
	switch {
	case meta.Environment == core.EnvDev || meta.Environment == core.EnvShared:
		return pulumi.String("http://localhost:5173").ToStringOutput()
	case meta.Environment.IsPreview():
		return pulumi.String(fmt.Sprintf("https://%s-%s-%s.vercel.app", vProj, meta.Environment.PreviewID(), team)).ToStringOutput()
	case meta.Environment == core.EnvStage:
		if spec.StageDomain != "" {
			return pulumi.String(NormalizeIssuerURL(spec.StageDomain)).ToStringOutput()
		}
		return pulumi.String(fmt.Sprintf("https://%s-stage-%s.vercel.app", vProj, team)).ToStringOutput()
	default:
		if spec.ProdDomain != "" {
			return pulumi.String(NormalizeIssuerURL(spec.ProdDomain)).ToStringOutput()
		}
		return pulumi.String(fmt.Sprintf("https://%s-%s.vercel.app", vProj, team)).ToStringOutput()
	}
}

func joinOriginPath(origin pulumi.StringOutput, path string) pulumi.StringOutput {
	return origin.ApplyT(func(base string) string {
		return strings.TrimRight(base, "/") + path
	}).(pulumi.StringOutput)
}

func buildOidcLink(meta core.ResourceMeta, spec OidcApp, res *OidcAppResource) core.LinkDescriptor {
	envMap := map[string]pulumi.StringOutput{
		"KINDE_ISSUER_URL":               res.IssuerURL,
		"KINDE_CLIENT_ID":                res.ClientID,
		"KINDE_SITE_URL":                 res.SiteURL,
		"KINDE_REDIRECT_URL":             res.RedirectURL,
		"KINDE_POST_LOGIN_REDIRECT_URL":  res.PostLoginRedirectURL,
		"KINDE_POST_LOGOUT_REDIRECT_URL": res.PostLogoutRedirectURL,
	}
	for k, v := range spec.Env {
		envMap[k] = v.ToStringOutput()
	}
	secrets := map[string]pulumi.StringOutput{
		"KINDE_CLIENT_SECRET": res.ClientSecret,
	}
	for k, v := range spec.Secrets {
		secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return core.LinkDescriptor{
		Name:    meta.Name,
		Kind:    meta.Kind,
		Env:     envMap,
		Secrets: secrets,
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
