package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
		forceDestroy := true

		plat, err := openbase.LookupPlatform(ctx)
		if err != nil {
			return err
		}
		gcpAcc := gcp.LookupAccount(plat, "gcp")
		if _, err := gcp.NewProject(ctx, "gcp", &gcp.ProjectArgs{
			ProjectName: appProject,
			Account:     gcpAcc,
		}); err != nil {
			return err
		}
		if _, err := vercel.NewProject(ctx, "vercel", &vercel.ProjectArgs{
			ProjectName:          appProject,
			Account:              vercel.LookupAccount(plat, "vercel"),
			VercelProjectName:    "openbase-e2e-" + appProject,
			SkipStageEnvironment: true,
		}); err != nil {
			return err
		}
		_, err = gcp.NewBucket(ctx, "shared-assets", &gcp.BucketArgs{
			ProjectName:     appProject,
			Account:         gcpAcc,
			BucketNamespace: gcpProject,
			ForceDestroy:    &forceDestroy,
		})
		return err
	})
}
