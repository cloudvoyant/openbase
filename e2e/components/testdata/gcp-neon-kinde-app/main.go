package main

import (
	_ "embed"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/composite"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

//go:embed app/server.py
var cloudRunServerPy string

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obgapp")
		gcpProject := envOrDefault("OPENBASE_GCP_PROJECT", "OPENBASE_TEST_GCP_PROJECT", "cloudvoyant-openbase")
		projectNum := envOrDefault("OPENBASE_GCP_PROJECT_NUMBER", "OPENBASE_TEST_GCP_PROJECT_NUMBER", "785485436615")

		_, err := composite.GcpCloudRunNeonApp{
			Name:             "web",
			Project:          appProject,
			GCPProject:       gcpProject,
			GCPProjectNumber: projectNum,
			PoolSuffix:       os.Getenv("OPENBASE_TEST_RUN_ID"),
			SkipKeyRing:      true,
			NeonOrgID:        os.Getenv("NEON_ORG_ID"),
			Image:            pulumi.String("mirror.gcr.io/library/python:3.12-slim"),
			Command:          []string{"python3", "-c", cloudRunServerPy},
			Auth:             envKindeConfig("openbase-e2e-" + appProject),
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
