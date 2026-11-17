package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		env, err := core.ResolveEnvironment(ctx, "")
		if err != nil {
			return err
		}
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		if appProject == "" {
			appProject = "obkinde"
		}
		teamSlug := os.Getenv("OPENBASE_TEST_TEAM_SLUG")
		if teamSlug == "" {
			teamSlug = "cloudvoyant"
		}
		if env == core.EnvShared {
			return deploySharedKinde(ctx, appProject, teamSlug)
		}
		return deployEnvKinde(ctx, env, appProject, teamSlug)
	})
}

func deploySharedKinde(ctx *pulumi.Context, appProject, teamSlug string) error {
	var extraDev []string
	if os.Getenv("OPENBASE_TEST_REVISION") == "b" {
		extraDev = []string{"http://localhost:4173/api/auth/kinde_callback"}
	}
	appName := os.Getenv("OPENBASE_TEST_KINDE_APP_NAME")
	if appName == "" {
		appName = "openbase-e2e-" + appProject
	}
	shared, err := kinde.NewSharedAuth(ctx, "shared", kinde.SharedAuthArgs{
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
	})
	if err != nil {
		return err
	}
	ctx.Export("devIssuerUrl", shared.DevIssuerURL)
	ctx.Export("devAppId", shared.DevAppID)
	ctx.Export("devClientId", shared.DevClientID)
	ctx.Export("devClientSecret", shared.DevClientSecret)
	ctx.Export("prodIssuerUrl", shared.ProdIssuerURL)
	ctx.Export("prodAppId", shared.ProdAppID)
	ctx.Export("prodClientId", shared.ProdClientID)
	ctx.Export("prodClientSecret", shared.ProdClientSecret)
	ctx.Export("emailPasswordConn", pulumi.String(shared.EmailPasswordConn))
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
	auth, err := kinde.NewAuth(ctx, "auth", kinde.AuthArgs{
		StatelessResourceArgs: core.StatelessResourceArgs{Project: appProject},
		IssuerURL:             pulumi.String(issuer),
		ClientID:              pulumi.String(clientID),
		ClientSecret:          pulumi.String(clientSec),
		VercelProjectName:     "openbase-" + appProject,
		VercelTeamSlug:        teamSlug,
	})
	if err != nil {
		return err
	}
	ctx.Export("tenant", pulumi.String(auth.Tenant))
	ctx.Export("issuerUrl", auth.IssuerURL)
	ctx.Export("clientId", auth.ClientID)
	ctx.Export("redirectUrl", auth.RedirectURL)
	return nil
}
