package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/composite"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obvapp")
		vProjName := "openbase-e2e-" + appProject
		teamID := envOrDefault("VERCEL_TEAM_ID", "OPENBASE_TEST_VERCEL_TEAM_ID", "")
		teamSlug := envOrDefault("VERCEL_TEAM_SLUG", "OPENBASE_TEST_VERCEL_TEAM_SLUG", "cloudvoyant")
		blobRW := envOrDefault("BLOB_READ_WRITE_TOKEN", "OPENBASE_TEST_BLOB_RW_TOKEN", "")

		_, err := composite.VercelNeonApp{
			Name:               "web",
			Project:            appProject,
			VercelProjectName:  vProjName,
			TeamID:             teamID,
			TeamSlug:           teamSlug,
			NeonOrgID:          os.Getenv("NEON_ORG_ID"),
			SourceDir:          "site",
			PathPrefix:         "site",
			BlobReadWriteToken: optionalString(blobRW),
			Auth:               envKindeConfig(vProjName),
		}.Register(ctx)
		return err
	})
}

func envKindeConfig(defaultAppName string) *composite.KindeConfig {
	return &composite.KindeConfig{
		AppName:                    envOrDefault("OPENBASE_KINDE_APP_NAME", "OPENBASE_TEST_KINDE_APP_NAME", defaultAppName),
		ImportDevAppID:             envOrDefault("OPENBASE_IMPORT_DEV_APP_ID", "OPENBASE_TEST_IMPORT_DEV_APP_ID", ""),
		ImportProdAppID:            envOrDefault("OPENBASE_IMPORT_PROD_APP_ID", "OPENBASE_TEST_IMPORT_PROD_APP_ID", ""),
		RetainApplicationsOnDelete: os.Getenv("OPENBASE_RETAIN_KINDE_APPS") == "1" || os.Getenv("OPENBASE_TEST_RETAIN_KINDE_APPS") == "1",
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
	}
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

func optionalString(val string) pulumi.StringInput {
	if val == "" {
		return nil
	}
	return pulumi.String(val)
}
