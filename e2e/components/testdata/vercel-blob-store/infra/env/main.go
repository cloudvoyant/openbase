package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		shared, err := openbase.LookupShared(ctx)
		if err != nil {
			return err
		}
		branchFrom := ""
		if os.Getenv("OPENBASE_TEST_REVISION") == "v2" {
			branchFrom = "prod"
		}
		_, err = vercel.NewBlobStore(ctx, "uploads", &vercel.BlobStoreArgs{
			ProjectName: os.Getenv("OPENBASE_TEST_APP_PROJECT"),
			Project:     vercel.LookupProject(shared, "vercel"),
			TeamID:      os.Getenv("OPENBASE_TEST_VERCEL_TEAM_ID"),
			BranchFrom:  branchFrom,
		})
		return err
	})
}
