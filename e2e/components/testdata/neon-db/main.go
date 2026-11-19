package main

import (
	"errors"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/neon"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		if os.Getenv("OPENBASE_TEST_FAIL_INFRA") == "1" {
			return errors.New("simulated neon infrastructure program failure")
		}
		env, err := core.ResolveEnvironment(ctx, "")
		if err != nil {
			return err
		}
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		if appProject == "" {
			appProject = "obneon"
		}
		if env == core.EnvShared {
			proj, err := neon.Project{
				Project:  appProject,
				OrgID:    os.Getenv("NEON_ORG_ID"),
				RegionID: "aws-us-east-1",
			}.Register(ctx)
			if err != nil {
				return err
			}
			ctx.Export("projectId", proj.ProjectID)
			ctx.Export("defaultBranchId", proj.DefaultBranchID)
			return nil
		}
		branchFrom := ""
		if env == core.EnvStage {
			branchFrom = "prod"
		} else if env.IsPreview() {
			branchFrom = "dev"
		}
		var parentBranch pulumi.StringInput
		if p := os.Getenv("OPENBASE_TEST_PARENT_BRANCH_ID"); p != "" {
			parentBranch = pulumi.String(p)
		}
		db, err := neon.Database{
			Name:            "db",
			Project:         appProject,
			BranchFrom:      branchFrom,
			ProjectID:       pulumi.String(os.Getenv("OPENBASE_TEST_NEON_PROJECT_ID")),
			DefaultBranchID: pulumi.String(os.Getenv("OPENBASE_TEST_DEFAULT_BRANCH_ID")),
			ParentBranchID:  parentBranch,
		}.Register(ctx)
		if err != nil {
			return err
		}
		ctx.Export("branchId", db.BranchID)
		ctx.Export("databaseUrl", db.DatabaseURL)
		ctx.Export("directUrl", db.DirectURL)
		return nil
	})
}
