// Package composite provides opinionated Premise-aligned shared and per-environment OpenBase application blueprints.
package composite

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

// Public API -----------------------------------------------------------------

// KindeSharedConfig configures optional two-tenant Kinde OIDC applications on a shared platform stack (infra/shared).
type KindeSharedConfig struct {
	AppName                    string
	Dev                        kinde.TenantCredentials
	Prod                       kinde.TenantCredentials
	CallbackPath               string
	ExtraDevRedirectURIs       []string
	ExtraDevLogoutURIs         []string
	ExtraProdRedirectURIs      []string
	ExtraProdLogoutURIs        []string
	ImportDevAppID             string
	ImportProdAppID            string
	RetainApplicationsOnDelete bool
	SkipEmailPasswordConn      bool
}

// KindeAppConfig configures optional per-environment Kinde OIDC wiring from SharedStackRef on an environment stack (infra/).
type KindeAppConfig struct {
	CallbackPath string
}

// Private helpers ------------------------------------------------------------

func defaultCompStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

func resolveSharedStackRef(ctx *pulumi.Context, explicit string) string {
	if explicit != "" {
		return explicit
	}
	return fmt.Sprintf("%s/%s-shared/shared", ctx.Organization(), ctx.Project())
}

func requireSharedEnv(meta core.ResourceMeta) error {
	if meta.Environment != core.EnvShared {
		return fmt.Errorf("%w: %q requires the shared stack, got %q", core.ErrInvalidEnvironment, meta.Kind, meta.Environment)
	}
	return nil
}

func requireNonSharedEnv(meta core.ResourceMeta, sharedComponent string) error {
	if meta.Environment == core.EnvShared {
		return fmt.Errorf("%w: %q is per-environment; declare %s in infra/shared for the shared stack", core.ErrInvalidEnvironment, meta.Kind, sharedComponent)
	}
	return nil
}

func resolveDBBranch(ctx *pulumi.Context, env core.Environment) (string, string) {
	if env == core.EnvStage {
		return "prod", fmt.Sprintf("%s/%s/prod", ctx.Organization(), ctx.Project())
	}
	if env.IsPreview() {
		return "dev", fmt.Sprintf("%s/%s/dev", ctx.Organization(), ctx.Project())
	}
	return "", ""
}

func resolvePreviewStorageBranch(ctx *pulumi.Context, env core.Environment) (string, string) {
	if env.IsPreview() {
		return "dev", fmt.Sprintf("%s/%s/dev", ctx.Organization(), ctx.Project())
	}
	return "", ""
}

func exportKindeSharedOutputs(ctx *pulumi.Context, k *kinde.SharedOidcAppResource) {
	if k == nil {
		return
	}
	ctx.Export("devIssuerUrl", k.DevIssuerURL)
	ctx.Export("devAppId", k.DevAppID)
	ctx.Export("devClientId", k.DevClientID)
	ctx.Export("devClientSecret", k.DevClientSecret)
	ctx.Export("prodIssuerUrl", k.ProdIssuerURL)
	ctx.Export("prodAppId", k.ProdAppID)
	ctx.Export("prodClientId", k.ProdClientID)
	ctx.Export("prodClientSecret", k.ProdClientSecret)
}

func exportKindeEnvOutputs(ctx *pulumi.Context, auth *kinde.OidcAppResource) {
	if auth == nil {
		return
	}
	ctx.Export("kindeIssuerUrl", auth.IssuerURL)
	ctx.Export("kindeRedirectUrl", auth.RedirectURL)
}
