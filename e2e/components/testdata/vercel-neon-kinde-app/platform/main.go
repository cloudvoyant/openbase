package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		teamID := envOrDefault("VERCEL_TEAM_ID", "OPENBASE_TEST_VERCEL_TEAM_ID", "")
		teamSlug := envOrDefault("VERCEL_TEAM_SLUG", "OPENBASE_TEST_VERCEL_TEAM_SLUG", "cloudvoyant")
		baseDomain := os.Getenv("OPENBASE_BASE_DOMAIN")

		_, err := openbase.Platform{
			Org: teamSlug,
			Resources: []core.PlatformSpecItem{
				vercel.Platform{
					TeamID:     teamID,
					TeamSlug:   teamSlug,
					BaseDomain: baseDomain,
				},
				neon.Platform{
					OrgID:    os.Getenv("NEON_ORG_ID"),
					RegionID: "aws-us-east-1",
				},
				kinde.Platform{
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
				},
			},
		}.Register(ctx)
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
