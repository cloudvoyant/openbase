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
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obvapp")
		vProjName := "openbase-e2e-" + appProject
		teamID := envOrDefault("VERCEL_TEAM_ID", "OPENBASE_TEST_VERCEL_TEAM_ID", "")
		teamSlug := envOrDefault("VERCEL_TEAM_SLUG", "OPENBASE_TEST_VERCEL_TEAM_SLUG", "cloudvoyant")
		baseDomain := os.Getenv("OPENBASE_BASE_DOMAIN")
		blobRW := envOrDefault("BLOB_READ_WRITE_TOKEN", "OPENBASE_TEST_BLOB_RW_TOKEN", "")

		env, err := openbase.NewEnvironment(ctx, openbase.Environment{Project: appProject})
		if err != nil {
			return err
		}
		db, err := openbase.Add(env, neon.Database{
			Name:   "db",
			APIKey: optionalString(os.Getenv("NEON_API_KEY")),
		})
		if err != nil {
			return err
		}
		uploads, err := openbase.Add(env, vercel.BlobStore{
			Name:               "uploads",
			TeamID:             teamID,
			BlobReadWriteToken: optionalString(blobRW),
		})
		if err != nil {
			return err
		}
		auth, err := openbase.Add(env, kinde.OidcApp{
			Name:              "auth",
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
		_, err = openbase.Add(env, vercel.WebApp{
			Name:              "web",
			VercelProjectName: vProjName,
			TeamID:            teamID,
			TeamSlug:          teamSlug,
			BaseDomain:        baseDomain,
			SourceDir:         "../../apps/web",
			Links:             []core.Resource{uploads},
			Env: map[string]pulumi.StringInput{
				"OPENBASE_ENVIRONMENT":           pulumi.String(env.OpenBaseMeta().Environment.String()),
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
