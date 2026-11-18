package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/composite"
	"os"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obvapp")
		vProjName := "openbase-e2e-" + appProject
		teamID := envOrDefault("VERCEL_TEAM_ID", "OPENBASE_TEST_VERCEL_TEAM_ID", "")
		teamSlug := envOrDefault("VERCEL_TEAM_SLUG", "OPENBASE_TEST_VERCEL_TEAM_SLUG", "cloudvoyant")
		blobRW := envOrDefault("BLOB_READ_WRITE_TOKEN", "OPENBASE_TEST_BLOB_RW_TOKEN", "")

		_, err := composite.VercelNeonApp{
			Name:               "web",
			Project:            appProject,
			VercelProjectName:  vProjName,
			TeamID:             teamID,
			TeamSlug:           teamSlug,
			SourceDir:          "../apps/web",
			BlobReadWriteToken: optionalString(blobRW),
			Auth:               &composite.KindeAppConfig{},
		}.Register(ctx)
		return err
	})
}

func envOrDefault(primaryKey, secondaryKey, fallback string) string {
	if v := os.Getenv(primaryKey); v != "" {
		return v
	}
	if secondaryKey != "" {
		if v := os.Getenv(secondaryKey); v != "" {
			return v
		}
	}
	return fallback
}

func optionalString(val string) pulumi.StringInput {
	if val == "" {
		return nil
	}
	return pulumi.String(val)
}
