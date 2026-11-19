package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		teamID := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_ID")
		teamSlug := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_SLUG")
		env, err := core.ResolveEnvironment(ctx, "")
		if err != nil {
			return err
		}
		if env == core.EnvShared {
			proj, err := vercel.Project{
				Project:           appProject,
				VercelProjectName: "openbase-e2e-" + appProject,
				TeamID:            teamID,
				TeamSlug:          teamSlug,
				EnableBlobStore:   true,
			}.Register(ctx)
			if err != nil {
				return err
			}
			ctx.Export("projectId", proj.ProjectID)
			ctx.Export("stageCustomEnvironmentId", proj.StageCustomEnvironmentID)
			ctx.Export("blobStoreId", proj.BlobStoreID)
			ctx.Export("blobBaseUrl", proj.BlobBaseURL)
			return nil
		}
		branchFrom := ""
		if env.IsPreview() {
			branchFrom = "dev"
		}
		var rwTok pulumi.StringInput
		if t := os.Getenv("OPENBASE_TEST_BLOB_RW_TOKEN"); t != "" {
			rwTok = pulumi.String(t)
		}
		blob, err := vercel.BlobStore{
			Name:                     "uploads",
			Project:                  appProject,
			BranchFrom:               branchFrom,
			StoreID:                  pulumi.String(os.Getenv("OPENBASE_TEST_BLOB_STORE_ID")),
			VercelProjectID:          pulumi.String(os.Getenv("OPENBASE_TEST_VERCEL_PROJECT_ID")),
			TeamID:                   teamID,
			StageCustomEnvironmentID: pulumi.String(os.Getenv("OPENBASE_TEST_STAGE_ENV_ID")),
			BlobReadWriteToken:       rwTok,
		}.Register(ctx)
		if err != nil {
			return err
		}
		ctx.Export("blobBaseUrl", blob.BlobBaseURL)
		ctx.Export("blobPrefix", pulumi.String(blob.BlobPrefix))
		ctx.Export("blobFallbackPrefix", pulumi.String(blob.BlobFallbackPrefix))
		return nil
	})
}
