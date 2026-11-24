package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		_, err := kinde.NewProject(ctx, "kinde", &kinde.ProjectArgs{
			ProjectName: appProject,
			AppName:     os.Getenv("OPENBASE_TEST_KINDE_APP_NAME"),
			NonProd: kinde.EnvironmentCredentials{
				Domain:       os.Getenv("KINDE_NONPROD_TENANT_DOMAIN"),
				ClientID:     pulumi.String(os.Getenv("KINDE_NONPROD_M2M_CLIENT_ID")),
				ClientSecret: pulumi.String(os.Getenv("KINDE_NONPROD_M2M_CLIENT_SECRET")),
			},
			Prod: kinde.EnvironmentCredentials{
				Domain:       os.Getenv("KINDE_PROD_TENANT_DOMAIN"),
				ClientID:     pulumi.String(os.Getenv("KINDE_PROD_M2M_CLIENT_ID")),
				ClientSecret: pulumi.String(os.Getenv("KINDE_PROD_M2M_CLIENT_SECRET")),
			},
			ImportNonProdAppID:         os.Getenv("OPENBASE_TEST_IMPORT_NONPROD_APP_ID"),
			ImportProdAppID:            os.Getenv("OPENBASE_TEST_IMPORT_PROD_APP_ID"),
			RetainApplicationsOnDelete: os.Getenv("OPENBASE_TEST_RETAIN_KINDE_APPS") == "1",
		})
		return err
	})
}
