package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(deployVercelNeonKindeEnv)
}

func deployVercelNeonKindeEnv(ctx *pulumi.Context) error {
	appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obvapp")
	vProjName := "openbase-e2e-" + appProject
	teamID := envOrDefault("VERCEL_TEAM_ID", "OPENBASE_TEST_VERCEL_TEAM_ID", "")
	teamSlug := envOrDefault("VERCEL_TEAM_SLUG", "OPENBASE_TEST_VERCEL_TEAM_SLUG", "cloudvoyant")
	baseDomain := os.Getenv("OPENBASE_BASE_DOMAIN")
	blobRW := envOrDefault("BLOB_READ_WRITE_TOKEN", "OPENBASE_TEST_BLOB_RW_TOKEN", "")

	tier, err := core.ParseEnvironment(ctx.Stack())
	if err != nil {
		return err
	}
	shared, err := openbase.LookupShared(ctx)
	if err != nil {
		return err
	}
	return registerVercelEnvResources(ctx, shared, tier, appProject, vProjName, teamID, teamSlug, baseDomain, blobRW)
}

func registerVercelEnvResources(
	ctx *pulumi.Context,
	shared *core.ScopeRef,
	tier core.Environment,
	appProject, vProjName, teamID, teamSlug, baseDomain, blobRW string,
) error {
	db, err := neon.NewDatabase(ctx, "db", &neon.DatabaseArgs{
		Project: appProject,
		Shared:  shared,
		APIKey:  optionalString(os.Getenv("NEON_API_KEY")),
	})
	if err != nil {
		return err
	}
	uploads, err := vercel.NewBlobStore(ctx, "uploads", &vercel.BlobStoreArgs{
		Project:            appProject,
		Shared:             shared,
		TeamID:             teamID,
		BlobReadWriteToken: optionalString(blobRW),
	})
	if err != nil {
		return err
	}
	auth, err := kinde.NewOidcApp(ctx, "auth", &kinde.OidcAppArgs{
		Project:           appProject,
		Shared:            shared,
		VercelProjectName: vProjName,
		VercelTeamSlug:    teamSlug,
		BaseDomain:        baseDomain,
	})
	if err != nil {
		return err
	}
	appSecrets := map[string]pulumi.StringInput{
		"DATABASE_URL":        db.DatabaseURL,
		"DIRECT_URL":          db.DirectURL,
		"KINDE_CLIENT_SECRET": auth.ClientSecret,
	}
	if blobRW != "" {
		appSecrets["BLOB_READ_WRITE_TOKEN"] = pulumi.String(blobRW)
	}
	_, err = vercel.NewWebApp(ctx, "web", &vercel.WebAppArgs{
		Project:           appProject,
		Shared:            shared,
		VercelProjectName: vProjName,
		TeamID:            teamID,
		TeamSlug:          teamSlug,
		BaseDomain:        baseDomain,
		SourceDir:         "../../apps/web",
		Links:             []core.Resource{uploads},
		Env: map[string]pulumi.StringInput{
			"OPENBASE_ENVIRONMENT":           pulumi.String(tier.String()),
			"BLOB_BASE_URL":                  uploads.BlobBaseURL,
			"BLOB_PREFIX":                    pulumi.String(uploads.BlobPrefix),
			"BLOB_FALLBACK_PREFIX":           pulumi.String(uploads.BlobFallbackPrefix),
			"KINDE_ISSUER_URL":               auth.IssuerURL,
			"KINDE_CLIENT_ID":                auth.ClientID,
			"KINDE_SITE_URL":                 auth.SiteURL,
			"KINDE_REDIRECT_URL":             auth.RedirectURL,
			"KINDE_POST_LOGIN_REDIRECT_URL":  auth.PostLoginRedirectURL,
			"KINDE_POST_LOGOUT_REDIRECT_URL": auth.PostLogoutRedirectURL,
		},
		Secrets: appSecrets,
	})
	return err
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
