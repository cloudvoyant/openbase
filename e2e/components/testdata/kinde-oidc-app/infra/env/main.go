package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		shared, err := openbase.LookupShared(ctx)
		if err != nil {
			return err
		}
		cbPath := "/api/auth/kinde_callback"
		if os.Getenv("OPENBASE_TEST_REVISION") == "v2" {
			cbPath = "/api/auth/callback"
		}
		_, err = kinde.NewOidcApp(ctx, "auth", &kinde.OidcAppArgs{
			ProjectName:  os.Getenv("OPENBASE_TEST_APP_PROJECT"),
			Project:      kinde.LookupProject(shared, "kinde"),
			CallbackPath: cbPath,
		})
		return err
	})
}
