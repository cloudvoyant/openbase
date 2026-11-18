package main

import (
	"fmt"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		env, err := core.ResolveEnvironment(ctx, "")
		if err != nil {
			return err
		}
		if env == core.EnvShared {
			return deployShared(ctx)
		}
		return deployEnvironment(ctx, env)
	})
}

func deployShared(ctx *pulumi.Context) error {
	appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obvapp")
	vProjName := "openbase-e2e-" + appProject
	teamID := envOrDefault("VERCEL_TEAM_ID", "OPENBASE_TEST_VERCEL_TEAM_ID", "")
	teamSlug := envOrDefault("VERCEL_TEAM_SLUG", "OPENBASE_TEST_VERCEL_TEAM_SLUG", "cloudvoyant")

	vercelShared, err := vercel.Shared{
		Name:              "vercel",
		Project:           appProject,
		VercelProjectName: vProjName,
		TeamID:            teamID,
		TeamSlug:          teamSlug,
		EnableBlobStore:   true,
	}.Register(ctx)
	if err != nil {
		return err
	}
	neonShared, err := neon.SharedProject{
		Name:     "neon",
		Project:  appProject,
		OrgID:    os.Getenv("NEON_ORG_ID"),
		RegionID: "aws-us-east-1",
	}.Register(ctx)
	if err != nil {
		return err
	}
	kindeShared, err := registerSharedKinde(ctx, appProject, vProjName, teamSlug)
	if err != nil {
		return err
	}
	exportShared(ctx, vercelShared, neonShared, kindeShared)
	return nil
}

func registerSharedKinde(ctx *pulumi.Context, appProject, vProjName, teamSlug string) (*kinde.SharedOidcAppResource, error) {
	appName := envOrDefault("OPENBASE_KINDE_APP_NAME", "OPENBASE_TEST_KINDE_APP_NAME", vProjName)
	importDev := envOrDefault("OPENBASE_IMPORT_DEV_APP_ID", "OPENBASE_TEST_IMPORT_DEV_APP_ID", "")
	importProd := envOrDefault("OPENBASE_IMPORT_PROD_APP_ID", "OPENBASE_TEST_IMPORT_PROD_APP_ID", "")
	retain := os.Getenv("OPENBASE_RETAIN_KINDE_APPS") == "1" || os.Getenv("OPENBASE_TEST_RETAIN_KINDE_APPS") == "1"

	return kinde.SharedOidcApp{
		Name:                       "kinde",
		Project:                    appProject,
		AppName:                    appName,
		VercelProjectName:          vProjName,
		VercelTeamSlug:             teamSlug,
		ImportDevAppID:             importDev,
		ImportProdAppID:            importProd,
		RetainApplicationsOnDelete: retain,
		Dev: kinde.TenantCredentials{
			Domain:       os.Getenv("KINDE_DEV_TENANT_DOMAIN"),
			ClientID:     pulumi.String(os.Getenv("KINDE_DEV_M2M_CLIENT_ID")),
			ClientSecret: pulumi.String(os.Getenv("KINDE_DEV_M2M_CLIENT_SECRET")),
		},
		Prod: kinde.TenantCredentials{
			Domain:       os.Getenv("KINDE_PROD_TENANT_DOMAIN"),
			ClientID:     pulumi.String(os.Getenv("KINDE_PROD_M2M_CLIENT_ID")),
			ClientSecret: pulumi.String(os.Getenv("KINDE_PROD_M2M_CLIENT_SECRET")),
		},
	}.Register(ctx)
}

func exportShared(
	ctx *pulumi.Context,
	v *vercel.SharedResource,
	n *neon.SharedProjectResource,
	k *kinde.SharedOidcAppResource,
) {
	ctx.Export("vercelProjectId", v.ProjectID)
	ctx.Export("vercelProjectName", v.VercelProjectName)
	ctx.Export("teamSlug", v.TeamSlug)
	ctx.Export("stageCustomEnvironmentId", v.StageCustomEnvironmentID)
	ctx.Export("protectionBypassSecret", v.ProtectionBypassSecret)
	ctx.Export("prodDomainName", v.ProdDomainName)
	ctx.Export("stageDomainName", v.StageDomainName)
	ctx.Export("blobStoreId", v.BlobStoreID)
	ctx.Export("blobBaseUrl", v.BlobBaseURL)
	ctx.Export("neonProjectId", n.ProjectID)
	ctx.Export("neonDefaultBranchId", n.DefaultBranchID)
	ctx.Export("devIssuerUrl", k.DevIssuerURL)
	ctx.Export("devAppId", k.DevAppID)
	ctx.Export("devClientId", k.DevClientID)
	ctx.Export("devClientSecret", k.DevClientSecret)
	ctx.Export("prodIssuerUrl", k.ProdIssuerURL)
	ctx.Export("prodAppId", k.ProdAppID)
	ctx.Export("prodClientId", k.ProdClientID)
	ctx.Export("prodClientSecret", k.ProdClientSecret)
}

func deployEnvironment(ctx *pulumi.Context, env core.Environment) error {
	appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obvapp")
	teamID := envOrDefault("VERCEL_TEAM_ID", "OPENBASE_TEST_VERCEL_TEAM_ID", "")
	sharedRef := fmt.Sprintf("%s/%s/shared", ctx.Organization(), ctx.Project())
	dbBranch, dbBranchRef := resolveDBBranch(ctx, env)
	blobBranch := ""
	if env.IsPreview() {
		blobBranch = "dev"
	}

	db, err := neon.Database{
		Name:           "db",
		Project:        appProject,
		BranchFrom:     dbBranch,
		SharedStackRef: sharedRef,
		BranchStackRef: dbBranchRef,
	}.Register(ctx)
	if err != nil {
		return err
	}
	blobs, err := vercel.BlobStore{
		Name:               "uploads",
		Project:            appProject,
		BranchFrom:         blobBranch,
		SharedStackRef:     sharedRef,
		TeamID:             teamID,
		BlobReadWriteToken: optionalString(envOrDefault("BLOB_READ_WRITE_TOKEN", "OPENBASE_TEST_BLOB_RW_TOKEN", "")),
	}.Register(ctx)
	if err != nil {
		return err
	}
	auth, err := kinde.OidcApp{
		Name:           "auth",
		Project:        appProject,
		SharedStackRef: sharedRef,
	}.Register(ctx)
	if err != nil {
		return err
	}
	web, err := vercel.WebApp{
		Name:           "web",
		Project:        appProject,
		Links:          []core.Resource{db, blobs, auth},
		SharedStackRef: sharedRef,
		TeamID:         teamID,
		SourceDir:      "site",
		PathPrefix:     "site",
	}.Register(ctx)
	if err != nil {
		return err
	}
	exportEnvironment(ctx, web, db, blobs, auth)
	return nil
}

func resolveDBBranch(ctx *pulumi.Context, env core.Environment) (string, string) {
	if env == core.EnvStage {
		return "prod", fmt.Sprintf("%s/%s/prod", ctx.Organization(), ctx.Project())
	}
	if env.IsPreview() {
		return "dev", fmt.Sprintf("%s/%s/dev", ctx.Organization(), ctx.Project())
	}
	return "", ""
}

func exportEnvironment(
	ctx *pulumi.Context,
	web *vercel.WebAppResource,
	db *neon.DatabaseResource,
	blobs *vercel.BlobStoreResource,
	auth *kinde.OidcAppResource,
) {
	ctx.Export("url", web.URL)
	ctx.Export("aliasHost", web.AliasHost)
	ctx.Export("deploymentId", web.DeploymentID)
	ctx.Export("branchId", db.BranchID)
	ctx.Export("databaseUrl", db.DatabaseURL)
	ctx.Export("blobBaseUrl", blobs.BlobBaseURL)
	ctx.Export("blobPrefix", pulumi.String(blobs.BlobPrefix))
	ctx.Export("blobFallbackPrefix", pulumi.String(blobs.BlobFallbackPrefix))
	ctx.Export("kindeIssuerUrl", auth.IssuerURL)
	ctx.Export("kindeRedirectUrl", auth.RedirectURL)
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
