package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		projectNum := os.Getenv("OPENBASE_TEST_GCP_PROJECT_NUMBER")
		baseDomain := os.Getenv("OPENBASE_BASE_DOMAIN")

		plat, err := openbase.LookupPlatform(ctx, "organization/platform/platform")
		if err != nil {
			return err
		}
		cb, err := gcp.BuildCloudRunCallbackURLs(appProject, "web", projectNum, "us-central1", baseDomain, "")
		if err != nil {
			return err
		}
		if _, err := gcp.NewProject(ctx, "gcp", &gcp.ProjectArgs{
			ProjectName: appProject,
			Account:     gcp.LookupAccount(plat, "gcp"),
			BaseDomain:  baseDomain,
		}); err != nil {
			return err
		}
		if _, err := neon.NewProject(ctx, "neon", &neon.ProjectArgs{
			ProjectName: appProject,
			Account:     neon.LookupAccount(plat, "neon"),
		}); err != nil {
			return err
		}
		_, err = kinde.NewProject(ctx, "kinde", &kinde.ProjectArgs{
			ProjectName:                appProject,
			Account:                    kinde.LookupAccount(plat, "kinde"),
			AppName:                    os.Getenv("OPENBASE_TEST_KINDE_APP_NAME"),
			BaseDomain:                 baseDomain,
			StageDomain:                cb.StageHost,
			ProdDomain:                 cb.ProdHost,
			ExtraNonProdRedirectURIs:   cb.ExtraNonProdRedirects,
			ExtraNonProdLogoutURIs:     cb.ExtraNonProdLogouts,
			ExtraProdRedirectURIs:      cb.ExtraProdRedirect,
			ExtraProdLogoutURIs:        cb.ExtraProdLogout,
			ImportNonProdAppID:         os.Getenv("OPENBASE_TEST_IMPORT_NONPROD_APP_ID"),
			ImportProdAppID:            os.Getenv("OPENBASE_TEST_IMPORT_PROD_APP_ID"),
			RetainApplicationsOnDelete: os.Getenv("OPENBASE_TEST_RETAIN_KINDE_APPS") == "1",
		})
		return err
	})
}
