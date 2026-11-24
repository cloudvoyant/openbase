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
		if _, err := gcp.NewAccount(ctx, "gcp", &gcp.AccountArgs{
			Org:              "cloudvoyant",
			GCPProject:       pulumi.String(os.Getenv("OPENBASE_TEST_GCP_PROJECT")),
			GCPProjectNumber: os.Getenv("OPENBASE_TEST_GCP_PROJECT_NUMBER"),
			PoolSuffix:       os.Getenv("OPENBASE_TEST_RUN_ID"),
			BaseDomain:       os.Getenv("OPENBASE_BASE_DOMAIN"),
			Links:            []core.OIDCProviderSource{core.GitHubActionsOIDC("cloudvoyant")},
			SkipKeyRing:      true,
		}); err != nil {
			return err
		}
		if _, err := neon.NewAccount(ctx, "neon", &neon.AccountArgs{
			Org:             "cloudvoyant",
			OrgID:           os.Getenv("NEON_ORG_ID"),
			DefaultRegionID: "aws-us-east-1",
		}); err != nil {
			return err
		}
		_, err := kinde.NewAccount(ctx, "kinde", &kinde.AccountArgs{
			Org: "cloudvoyant",
			Environments: map[string]kinde.EnvironmentCredentials{
				"non-prod": {
					Domain:       os.Getenv("KINDE_NONPROD_TENANT_DOMAIN"),
					ClientID:     pulumi.String(os.Getenv("KINDE_NONPROD_M2M_CLIENT_ID")),
					ClientSecret: pulumi.String(os.Getenv("KINDE_NONPROD_M2M_CLIENT_SECRET")),
				},
				"prod": {
					Domain:       os.Getenv("KINDE_PROD_TENANT_DOMAIN"),
					ClientID:     pulumi.String(os.Getenv("KINDE_PROD_M2M_CLIENT_ID")),
					ClientSecret: pulumi.String(os.Getenv("KINDE_PROD_M2M_CLIENT_SECRET")),
				},
			},
		})
		return err
	})
}
