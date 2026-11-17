package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/synthetic"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		env, err := core.ResolveEnvironment(ctx, "")
		if err != nil {
			return err
		}
		branchFrom := ""
		if env.IsPreview() {
			branchFrom = "dev"
		}
		uploads, err := synthetic.Storage{
			Name:       "uploads",
			Project:    "storefront",
			BranchFrom: branchFrom,
		}.Register(ctx)
		if err != nil {
			return err
		}
		api, err := synthetic.Service{
			Name:    "api",
			Project: "storefront",
			Links:   []core.Resource{uploads},
		}.Register(ctx)
		if err != nil {
			return err
		}
		ctx.Export("apiUrl", api.URL)
		ctx.Export("uploadsBucket", uploads.BucketName)
		return nil
	})
}
