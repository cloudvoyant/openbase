package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		_, err := vercel.NewProject(ctx, "vercel", &vercel.ProjectArgs{
			ProjectName:          appProject,
			VercelProjectName:    "openbase-e2e-" + appProject,
			TeamID:               os.Getenv("OPENBASE_TEST_VERCEL_TEAM_ID"),
			TeamSlug:             os.Getenv("OPENBASE_TEST_VERCEL_TEAM_SLUG"),
			EnableBlobStore:      true,
			SkipStageEnvironment: true,
		})
		return err
	})
}
