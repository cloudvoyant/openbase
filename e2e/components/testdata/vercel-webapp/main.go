package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		env, err := core.ResolveEnvironment(ctx, "")
		if err != nil {
			return err
		}
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		vProjName := "openbase-e2e-" + appProject
		teamID := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_ID")
		teamSlug := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_SLUG")
		if env == core.EnvShared {
			proj, err := vercel.Project{
				Project:           appProject,
				VercelProjectName: vProjName,
				TeamID:            teamID,
				TeamSlug:          teamSlug,
			}.Register(ctx)
			if err != nil {
				return err
			}
			ctx.Export("projectId", proj.ProjectID)
			ctx.Export("stageCustomEnvironmentId", proj.StageCustomEnvironmentID)
			ctx.Export("protectionBypassSecret", proj.ProtectionBypassSecret)
			ctx.Export("prodDomainName", proj.ProdDomainName)
			ctx.Export("stageDomainName", proj.StageDomainName)
			return nil
		}
		revision := os.Getenv("OPENBASE_TEST_REVISION")
		if revision == "" {
			revision = "a"
		}
		app, err := vercel.WebApp{
			Name:    "web",
			Project: appProject,
			Env: map[string]pulumi.StringInput{
				"OPENBASE_REVISION": pulumi.String(revision),
				"SCOPED_TIER":       pulumi.String(env.String()),
			},
			VercelProjectID:          pulumi.String(os.Getenv("OPENBASE_TEST_VERCEL_PROJECT_ID")),
			VercelProjectName:        vProjName,
			TeamID:                   teamID,
			TeamSlug:                 teamSlug,
			StageCustomEnvironmentID: pulumi.String(os.Getenv("OPENBASE_TEST_STAGE_ENV_ID")),
			SourceDir:                "site",
			PathPrefix:               "site",
		}.Register(ctx)
		if err != nil {
			return err
		}
		ctx.Export("url", app.URL)
		ctx.Export("aliasHost", app.AliasHost)
		ctx.Export("deploymentId", app.DeploymentID)
		return nil
	})
}
