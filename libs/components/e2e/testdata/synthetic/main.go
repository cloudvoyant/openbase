package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/synthetic"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		explicitEnv := os.Getenv("OPENBASE_TEST_ENV_OVERRIDE")
		uploads, err := synthetic.NewStorage(ctx, "uploads", synthetic.StorageArgs{
			StatefulResourceArgs: core.StatefulResourceArgs{
				Project:     "storefront",
				Environment: explicitEnv,
				BranchFrom:  "dev",
			},
		})
		if err != nil {
			return err
		}
		api, err := synthetic.NewService(ctx, "api", synthetic.ServiceArgs{
			StatelessResourceArgs: core.StatelessResourceArgs{
				Project:     "storefront",
				Environment: explicitEnv,
				Links:       []core.Resource{uploads},
			},
		})
		if err != nil {
			return err
		}
		ctx.Export("apiUrl", api.URL)
		ctx.Export("bucket", uploads.BucketName)
		ctx.Export("fallbackBucket", uploads.FallbackBucketName)
		return nil
	})
}
