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
		rev := os.Getenv("OPENBASE_TEST_REVISION")
		if rev == "" {
			rev = "v1"
		}
		var apiToken pulumi.StringInput
		if tok := os.Getenv("VERCEL_API_TOKEN"); tok != "" {
			apiToken = pulumi.String(tok)
		}
		_, err = vercel.NewWebApp(ctx, "web", &vercel.WebAppArgs{
			ProjectName: os.Getenv("OPENBASE_TEST_APP_PROJECT"),
			Project:     vercel.LookupProject(shared, "vercel"),
			TeamID:      os.Getenv("OPENBASE_TEST_VERCEL_TEAM_ID"),
			TeamSlug:    os.Getenv("OPENBASE_TEST_VERCEL_TEAM_SLUG"),
			BaseDomain:  os.Getenv("OPENBASE_BASE_DOMAIN"),
			APIToken:    apiToken,
			SourceDir:   "../../apps/web",
			Revision:    rev,
		})
		return err
	})
}
