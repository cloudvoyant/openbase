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
		appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obgapp")
		projectNum := envOrDefault("OPENBASE_GCP_PROJECT_NUMBER", "OPENBASE_TEST_GCP_PROJECT_NUMBER", "785485436615")
		baseDomain := os.Getenv("OPENBASE_BASE_DOMAIN")
		platformRef := envOrDefault("OPENBASE_PLATFORM_STACK_REF", "", "organization/platform/platform")

		plat, err := openbase.LookupPlatform(ctx, platformRef)
		if err != nil {
			return err
		}
		cb, err := gcp.BuildCloudRunCallbackURLs(appProject, "web", projectNum, "us-central1", baseDomain, "")
		if err != nil {
			return err
		}
		if _, err := gcp.NewProject(ctx, "gcp", &gcp.ProjectArgs{
			ProjectName: appProject,
			Account:     gcp.LookupAccount(plat),
			BaseDomain:  baseDomain,
		}); err != nil {
			return err
		}
		if _, err := neon.NewProject(ctx, "neon", &neon.ProjectArgs{
			ProjectName: appProject,
			Account:     neon.LookupAccount(plat),
		}); err != nil {
			return err
		}
		_, err = kinde.NewProject(ctx, "kinde", &kinde.ProjectArgs{
			ProjectName:                appProject,
			Account:                    kinde.LookupAccount(plat),
			AppName:                    envOrDefault("OPENBASE_KINDE_APP_NAME", "OPENBASE_TEST_KINDE_APP_NAME", "openbase-e2e-"+appProject),
			BaseDomain:                 baseDomain,
			StageDomain:                cb.StageHost,
			ProdDomain:                 cb.ProdHost,
			ExtraNonProdRedirectURIs:   cb.ExtraNonProdRedirects,
			ExtraNonProdLogoutURIs:     cb.ExtraNonProdLogouts,
			ExtraProdRedirectURIs:      cb.ExtraProdRedirect,
			ExtraProdLogoutURIs:        cb.ExtraProdLogout,
			ImportNonProdAppID:         envOrDefault("OPENBASE_IMPORT_NONPROD_APP_ID", "OPENBASE_TEST_IMPORT_NONPROD_APP_ID", ""),
			ImportProdAppID:            envOrDefault("OPENBASE_IMPORT_PROD_APP_ID", "OPENBASE_TEST_IMPORT_PROD_APP_ID", ""),
			RetainApplicationsOnDelete: os.Getenv("OPENBASE_RETAIN_KINDE_APPS") == "1" || os.Getenv("OPENBASE_TEST_RETAIN_KINDE_APPS") == "1",
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
