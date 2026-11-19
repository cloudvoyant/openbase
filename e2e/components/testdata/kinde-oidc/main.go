package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		if appProject == "" {
			appProject = "obkinde"
		}
		teamSlug := os.Getenv("OPENBASE_TEST_TEAM_SLUG")
		if teamSlug == "" {
			teamSlug = "cloudvoyant"
		}
		if core.IsProjectStack(ctx) {
			return deployKindeProject(ctx, appProject, teamSlug)
		}
		env, err := core.ResolveEnvironment(ctx, "")
		if err != nil {
			return err
		}
		return deployEnvKinde(ctx, env, appProject, teamSlug)
	})
}

func deployKindeProject(ctx *pulumi.Context, appProject, teamSlug string) error {
	var extraDev []string
	if os.Getenv("OPENBASE_TEST_REVISION") == "b" {
		extraDev = []string{"http://localhost:4173/api/auth/kinde_callback"}
	}
	appName := os.Getenv("OPENBASE_TEST_KINDE_APP_NAME")
	if appName == "" {
		appName = "openbase-e2e-" + appProject
	}
	proj, err := kinde.Project{
		Project:                    appProject,
		AppName:                    appName,
		VercelProjectName:          "openbase-" + appProject,
		VercelTeamSlug:             teamSlug,
		ExtraDevRedirectURIs:       extraDev,
		ImportDevAppID:             os.Getenv("OPENBASE_TEST_IMPORT_DEV_APP_ID"),
		ImportProdAppID:            os.Getenv("OPENBASE_TEST_IMPORT_PROD_APP_ID"),
		RetainApplicationsOnDelete: os.Getenv("OPENBASE_TEST_RETAIN_KINDE_APPS") == "1",
		Dev: kinde.TenantCredentials{
			Domain:       os.Getenv("KINDE_DEV_TENANT_DOMAIN"),
			ClientID:     pulumi.String(os.Getenv("KINDE_DEV_M2M_CLIENT_ID")),
			ClientSecret: pulumi.String(os.Getenv("KINDE_DEV_M2M_CLIENT_SECRET")),
		},
		Prod: kinde.TenantCredentials{
			Domain:       os.Getenv("KINDE_PROD_TENANT_DOMAIN"),
			ClientID:     pulumi.String(os.Getenv("KINDE_PROD_M2M_CLIENT_ID")),
			ClientSecret: pulumi.String(os.Getenv("KINDE_PROD_M2M_CLIENT_SECRET")),
		},
	}.Register(ctx)
	if err != nil {
		return err
	}
	ctx.Export("devIssuerUrl", proj.DevIssuerURL)
	ctx.Export("devAppId", proj.DevAppID)
	ctx.Export("devClientId", proj.DevClientID)
	ctx.Export("devClientSecret", proj.DevClientSecret)
	ctx.Export("prodIssuerUrl", proj.ProdIssuerURL)
	ctx.Export("prodAppId", proj.ProdAppID)
	ctx.Export("prodClientId", proj.ProdClientID)
	ctx.Export("prodClientSecret", proj.ProdClientSecret)
	ctx.Export("emailPasswordConn", pulumi.String(proj.EmailPasswordConn))
	return nil
}

func deployEnvKinde(ctx *pulumi.Context, env core.Environment, appProject, teamSlug string) error {
	useProd := env == core.EnvStage || env == core.EnvProd
	issuer := os.Getenv("OPENBASE_TEST_DEV_ISSUER")
	clientID := os.Getenv("OPENBASE_TEST_DEV_CLIENT_ID")
	clientSec := os.Getenv("OPENBASE_TEST_DEV_CLIENT_SECRET")
	if useProd {
		issuer = os.Getenv("OPENBASE_TEST_PROD_ISSUER")
		clientID = os.Getenv("OPENBASE_TEST_PROD_CLIENT_ID")
		clientSec = os.Getenv("OPENBASE_TEST_PROD_CLIENT_SECRET")
	}
	oidc, err := kinde.OidcApp{
		Name:              "auth",
		Project:           appProject,
		IssuerURL:         pulumi.String(issuer),
		ClientID:          pulumi.String(clientID),
		ClientSecret:      pulumi.String(clientSec),
		VercelProjectName: "openbase-" + appProject,
		VercelTeamSlug:    teamSlug,
	}.Register(ctx)
	if err != nil {
		return err
	}
	ctx.Export("tenant", pulumi.String(oidc.Tenant))
	ctx.Export("issuerUrl", oidc.IssuerURL)
	ctx.Export("clientId", oidc.ClientID)
	ctx.Export("redirectUrl", oidc.RedirectURL)
	return nil
}
