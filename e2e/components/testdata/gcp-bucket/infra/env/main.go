package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/gcp"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
		rev := os.Getenv("OPENBASE_TEST_REVISION")
		if rev == "" {
			rev = "v1"
		}
		_, err := gcp.NewBucket(ctx, "uploads", &gcp.BucketArgs{
			ProjectName:     os.Getenv("OPENBASE_TEST_APP_PROJECT"),
			GCPProject:      pulumi.String(gcpProject),
			BucketNamespace: gcpProject,
			ExtraLabels:     map[string]string{"openbase-revision": rev},
		})
		return err
	})
}
