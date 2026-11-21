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

		plat, err := openbase.LookupPlatform(ctx, "organization/platform/platform")
		if err != nil {
			return err
		}
		if _, err := gcp.NewProject(ctx, "gcp-project", &gcp.ProjectArgs{
			Project:       appProject,
			PlatformStack: plat,
		}); err != nil {
			return err
		}
		if _, err := vercel.NewProject(ctx, "vercel-project", &vercel.ProjectArgs{
			Project:              appProject,
			PlatformStack:        plat,
			VercelProjectName:    "openbase-e2e-" + appProject,
			SkipStageEnvironment: true,
		}); err != nil {
			return err
		}
		_, err = gcp.NewBucket(ctx, "shared-assets", &gcp.BucketArgs{
			Project:         appProject,
			GCPProject:      pulumi.String(gcpProject),
			BucketNamespace: gcpProject,
			ForceDestroy:    &forceDestroy,
		})
		return err
	})
}
