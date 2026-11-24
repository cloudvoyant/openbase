package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/neon"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		_, err := neon.NewProject(ctx, "neon", &neon.ProjectArgs{
			ProjectName: os.Getenv("OPENBASE_TEST_APP_PROJECT"),
			OrgID:       os.Getenv("NEON_ORG_ID"),
		})
		return err
	})
}
