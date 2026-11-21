package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obvapp")
		vProjName := "openbase-e2e-" + appProject
		teamSlug := envOrDefault("VERCEL_TEAM_SLUG", "OPENBASE_TEST_VERCEL_TEAM_SLUG", "cloudvoyant")
		baseDomain := os.Getenv("OPENBASE_BASE_DOMAIN")
		platformRef := envOrDefault("OPENBASE_PLATFORM_STACK_REF", "", "organization/platform/platform")

		plat, err := openbase.LookupPlatform(ctx, platformRef)
		if err != nil {
			return err
		}
		authProj, err := kinde.NewProject(ctx, "kinde", &kinde.ProjectArgs{
			ProjectName:                appProject,
			Account:                    kinde.LookupAccount(plat),
			AppName:                    envOrDefault("OPENBASE_KINDE_APP_NAME", "OPENBASE_TEST_KINDE_APP_NAME", vProjName),
			VercelProjectName:          vProjName,
			VercelTeamSlug:             teamSlug,
			BaseDomain:                 baseDomain,
			ImportNonProdAppID:         envOrDefault("OPENBASE_IMPORT_NONPROD_APP_ID", "OPENBASE_TEST_IMPORT_NONPROD_APP_ID", ""),
			ImportProdAppID:            envOrDefault("OPENBASE_IMPORT_PROD_APP_ID", "OPENBASE_TEST_IMPORT_PROD_APP_ID", ""),
			RetainApplicationsOnDelete: os.Getenv("OPENBASE_RETAIN_KINDE_APPS") == "1" || os.Getenv("OPENBASE_TEST_RETAIN_KINDE_APPS") == "1",
		})
		if err != nil {
			return err
		}
		if _, err := neon.NewProject(ctx, "neon", &neon.ProjectArgs{
			ProjectName: appProject,
			Account:     neon.LookupAccount(plat),
		}); err != nil {
			return err
		}
		_, err = vercel.NewProject(ctx, "vercel", &vercel.ProjectArgs{
			ProjectName:       appProject,
			Account:           vercel.LookupAccount(plat),
			VercelProjectName: vProjName,
			EnableBlobStore:   true,
			PreviewEnv: map[string]pulumi.StringInput{
				"KINDE_ISSUER_URL": authProj.NonProdIssuerURL,
				"KINDE_CLIENT_ID":  authProj.NonProdClientID,
			},
			PreviewSecrets: map[string]pulumi.StringInput{
				"KINDE_CLIENT_SECRET": authProj.NonProdClientSecret,
			},
		})
		return err
	})
}

func envOrDefault(primaryKey, secondaryKey, fallback string) string {
	if v := os.Getenv(primaryKey); v != "" {
		return v
	}
	if secondaryKey != "" {
		if v := os.Getenv(secondaryKey); v != "" {
			return v
		}
	}
	return fallback
}
