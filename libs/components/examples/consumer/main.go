package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/synthetic"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		uploads, err := synthetic.NewStorage(ctx, "uploads", synthetic.StorageArgs{
			StatefulResourceArgs: core.StatefulResourceArgs{
				Project:    "storefront",
				BranchFrom: "dev",
			},
		})
		if err != nil {
			return err
		}
		api, err := synthetic.NewService(ctx, "api", synthetic.ServiceArgs{
			StatelessResourceArgs: core.StatelessResourceArgs{
				Project: "storefront",
				Links:   []core.Resource{uploads},
			},
		})
		if err != nil {
			return err
		}
		ctx.Export("apiUrl", api.URL)
		ctx.Export("uploadsBucket", uploads.BucketName)
		return nil
	})
}
