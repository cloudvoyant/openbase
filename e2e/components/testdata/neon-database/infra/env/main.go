package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/neon"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		shared, err := openbase.LookupShared(ctx)
		if err != nil {
			return err
		}
		suspend := 300
		if os.Getenv("OPENBASE_TEST_REVISION") == "v2" {
			suspend = 180
		}
		_, err = neon.NewDatabase(ctx, "db", &neon.DatabaseArgs{
			ProjectName:    os.Getenv("OPENBASE_TEST_APP_PROJECT"),
			Project:        neon.LookupProject(shared, "neon"),
			APIKey:         pulumi.String(os.Getenv("NEON_API_KEY")),
			SuspendSeconds: &suspend,
		})
		return err
	})
}
