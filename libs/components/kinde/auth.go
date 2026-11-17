package kinde

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*Auth)(nil)

// ErrAuthSourceRequired reports missing SharedAuth / SharedStackRef / explicit credentials on AuthArgs.
var ErrAuthSourceRequired = errors.New("kinde: shared, sharedStackRef, or explicit tenant outputs are required")

// AuthArgs configures per-environment Kinde OIDC credentials and redirect URLs.
type AuthArgs struct {
	core.StatelessResourceArgs
	Shared            *SharedAuth
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

// Auth resolves tier-appropriate Kinde tenant credentials and exports KINDE_* Links.
type Auth struct {
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

// NewAuth registers a per-environment Kinde Auth component satisfying core.StatelessResource.
func NewAuth(ctx *pulumi.Context, name string, args AuthArgs, opts ...pulumi.ResourceOption) (*Auth, error) {
	if !hasAuthSource(args) {
		return nil, ErrAuthSourceRequired
	}
	res := &Auth{}
	meta, err := core.InitStateless(ctx, "openbase:kinde:Auth", name, "kinde-auth", res, &res.StatelessMeta, args.StatelessResourceArgs, opts...)
	if err != nil {
		return nil, err
	}
	if err := populateAuthOutputs(ctx, name, meta, args, res); err != nil {
		return nil, err
	}
	res.SetLink(buildAuthLink(meta, args, res))
	if err := registerAuthOutputs(ctx, meta, res); err != nil {
		return nil, err
	}
	return res, nil
}

// Private helpers ------------------------------------------------------------

func hasAuthSource(args AuthArgs) bool {
	if args.Shared != nil || args.SharedStackRef != "" {
		return true
	}
	return args.IssuerURL != nil && args.ClientID != nil && args.ClientSecret != nil
}

func populateAuthOutputs(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args AuthArgs,
	res *Auth,
) error {
	useProd := meta.Environment == core.EnvStage || meta.Environment == core.EnvProd
	res.Tenant = "dev"
	if useProd {
		res.Tenant = "prod"
	}
	issuer, clientID, clientSecret, err := resolveTenantCredOutputs(ctx, name, useProd, args)
	if err != nil {
		return err
	}
	siteURL := resolveSiteURL(meta, args)
	cb := ensureLeadingSlash(defaultKindeStr(args.CallbackPath, "/api/auth/kinde_callback"))
	postLogin := ensureLeadingSlash(defaultKindeStr(args.PostLoginPath, "/"))
	postLogout := ensureLeadingSlash(defaultKindeStr(args.PostLogoutPath, "/"))

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
	args AuthArgs,
) (pulumi.StringOutput, pulumi.StringOutput, pulumi.StringOutput, error) {
	if args.IssuerURL != nil && args.ClientID != nil && args.ClientSecret != nil {
		return args.IssuerURL.ToStringOutput(), args.ClientID.ToStringOutput(), args.ClientSecret.ToStringOutput(), nil
	}
	if args.Shared != nil {
		if useProd {
			return args.Shared.ProdIssuerURL, args.Shared.ProdClientID, args.Shared.ProdClientSecret, nil
		}
		return args.Shared.DevIssuerURL, args.Shared.DevClientID, args.Shared.DevClientSecret, nil
	}
	sr, err := pulumi.NewStackReference(ctx, name+"-shared-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(args.SharedStackRef)})
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

func resolveSiteURL(meta core.ResourceMeta, args AuthArgs) pulumi.StringOutput {
	if args.SiteURL != nil {
		return args.SiteURL.ToStringOutput()
	}
	vProj := defaultKindeStr(args.VercelProjectName, meta.Project)
	team := defaultKindeStr(args.VercelTeamSlug, "local")
	switch {
	case meta.Environment == core.EnvDev || meta.Environment == core.EnvShared:
		return pulumi.String("http://localhost:5173").ToStringOutput()
	case meta.Environment.IsPreview():
		return pulumi.String(fmt.Sprintf("https://%s-%s-%s.vercel.app", vProj, meta.Environment.PreviewID(), team)).ToStringOutput()
	case meta.Environment == core.EnvStage:
		if args.StageDomain != "" {
			return pulumi.String(NormalizeIssuerURL(args.StageDomain)).ToStringOutput()
		}
		return pulumi.String(fmt.Sprintf("https://%s-stage-%s.vercel.app", vProj, team)).ToStringOutput()
	default:
		if args.ProdDomain != "" {
			return pulumi.String(NormalizeIssuerURL(args.ProdDomain)).ToStringOutput()
		}
		return pulumi.String(fmt.Sprintf("https://%s-%s.vercel.app", vProj, team)).ToStringOutput()
	}
}

func joinOriginPath(origin pulumi.StringOutput, path string) pulumi.StringOutput {
	return origin.ApplyT(func(base string) string {
		return strings.TrimRight(base, "/") + path
	}).(pulumi.StringOutput)
}

func buildAuthLink(meta core.ResourceMeta, args AuthArgs, res *Auth) core.LinkDescriptor {
	envMap := map[string]pulumi.StringOutput{
		"KINDE_ISSUER_URL":               res.IssuerURL,
		"KINDE_CLIENT_ID":                res.ClientID,
		"KINDE_SITE_URL":                 res.SiteURL,
		"KINDE_REDIRECT_URL":             res.RedirectURL,
		"KINDE_POST_LOGIN_REDIRECT_URL":  res.PostLoginRedirectURL,
		"KINDE_POST_LOGOUT_REDIRECT_URL": res.PostLogoutRedirectURL,
	}
	for k, v := range args.Env {
		envMap[k] = v.ToStringOutput()
	}
	secrets := map[string]pulumi.StringOutput{
		"KINDE_CLIENT_SECRET": res.ClientSecret,
	}
	for k, v := range args.Secrets {
		secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return core.LinkDescriptor{
		Name:    meta.Name,
		Kind:    meta.Kind,
		Env:     envMap,
		Secrets: secrets,
	}
}

func registerAuthOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *Auth) error {
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
