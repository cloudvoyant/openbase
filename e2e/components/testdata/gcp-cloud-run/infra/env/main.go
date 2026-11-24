package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/gcp"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		rev := os.Getenv("OPENBASE_TEST_REVISION")
		if rev == "" {
			rev = "v1"
		}
		_, err := gcp.NewCloudRunService(ctx, "web", &gcp.CloudRunServiceArgs{
			ProjectName:      os.Getenv("OPENBASE_TEST_APP_PROJECT"),
			GCPProject:       pulumi.String(os.Getenv("OPENBASE_TEST_GCP_PROJECT")),
			GCPProjectNumber: pulumi.String(os.Getenv("OPENBASE_TEST_GCP_PROJECT_NUMBER")),
			Image:            pulumi.String("us-docker.pkg.dev/cloudrun/container/hello"),
			Revision:         rev,
		})
		return err
	})
}
