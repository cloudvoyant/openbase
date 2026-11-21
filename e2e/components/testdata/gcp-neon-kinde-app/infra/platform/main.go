package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		gcpProject := envOrDefault("OPENBASE_GCP_PROJECT", "OPENBASE_TEST_GCP_PROJECT", "cloudvoyant-openbase")
		projectNum := envOrDefault("OPENBASE_GCP_PROJECT_NUMBER", "OPENBASE_TEST_GCP_PROJECT_NUMBER", "785485436615")

		if _, err := gcp.NewPlatform(ctx, "gcp-platform", &gcp.PlatformArgs{
			Org:              "cloudvoyant",
			GCPProject:       pulumi.String(gcpProject),
			GCPProjectNumber: projectNum,
			PoolSuffix:       os.Getenv("OPENBASE_TEST_RUN_ID"),
			BaseDomain:       os.Getenv("OPENBASE_BASE_DOMAIN"),
			Links:            []core.OIDCProviderSource{core.GitHubActionsOIDC("cloudvoyant")},
			SkipKeyRing:      true,
		}); err != nil {
			return err
		}
		if _, err := neon.NewPlatform(ctx, "neon-platform", &neon.PlatformArgs{
			Org:             "cloudvoyant",
			OrgID:           os.Getenv("NEON_ORG_ID"),
			DefaultRegionID: "aws-us-east-1",
		}); err != nil {
			return err
		}
		_, err := kinde.NewPlatform(ctx, "kinde-platform", &kinde.PlatformArgs{
			Org: "cloudvoyant",
			NonProd: kinde.TenantCredentials{
				Domain:       envOrDefault("KINDE_NONPROD_TENANT_DOMAIN", "KINDE_DEV_TENANT_DOMAIN", ""),
				ClientID:     pulumi.String(envOrDefault("KINDE_NONPROD_M2M_CLIENT_ID", "KINDE_DEV_M2M_CLIENT_ID", "")),
				ClientSecret: pulumi.String(envOrDefault("KINDE_NONPROD_M2M_CLIENT_SECRET", "KINDE_DEV_M2M_CLIENT_SECRET", "")),
			},
			Prod: kinde.TenantCredentials{
				Domain:       os.Getenv("KINDE_PROD_TENANT_DOMAIN"),
				ClientID:     pulumi.String(os.Getenv("KINDE_PROD_M2M_CLIENT_ID")),
				ClientSecret: pulumi.String(os.Getenv("KINDE_PROD_M2M_CLIENT_SECRET")),
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
