package main

import (
	_ "embed"
	"errors"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
)

//go:embed app/server.py
var cloudRunServerPy string

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		if os.Getenv("OPENBASE_TEST_FAIL_INFRA") == "1" {
			return errors.New("simulated infrastructure program failure")
		}
		appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obgapp")
		gcpProject := envOrDefault("OPENBASE_GCP_PROJECT", "OPENBASE_TEST_GCP_PROJECT", "cloudvoyant-openbase")
		projectNum := envOrDefault("OPENBASE_GCP_PROJECT_NUMBER", "OPENBASE_TEST_GCP_PROJECT_NUMBER", "785485436615")
		baseDomain := os.Getenv("OPENBASE_BASE_DOMAIN")
		revision := envOrDefault("OPENBASE_TEST_REVISION", "", "a")

		env, err := openbase.NewEnvironment(ctx, openbase.Environment{Project: appProject})
		if err != nil {
			return err
		}
		siteURL, err := resolveCloudRunSiteURL(appProject, env.OpenBaseMeta().Environment, projectNum, baseDomain)
		if err != nil {
			return err
		}
		db, err := openbase.Add(env, neon.Database{
			Name:   "db",
			APIKey: pulumi.String(os.Getenv("NEON_API_KEY")),
		})
		if err != nil {
			return err
		}
		uploads, err := openbase.Add(env, gcp.Bucket{
			Name:            "uploads",
			GCPProject:      pulumi.String(gcpProject),
			BucketNamespace: gcpProject,
			ExtraLabels:     map[string]string{"openbase-revision": revision},
		})
		if err != nil {
			return err
		}
		auth, err := openbase.Add(env, kinde.OidcApp{
			Name:    "auth",
			SiteURL: pulumi.String(siteURL),
		})
		if err != nil {
			return err
		}
		_, err = openbase.Add(env, gcp.CloudRunService{
			Name:             "web",
			GCPProject:       pulumi.String(gcpProject),
			GCPProjectNumber: pulumi.String(projectNum),
			BaseDomain:       baseDomain,
			Image:            pulumi.String("mirror.gcr.io/library/python:3.12-slim"),
			Command:          []string{"python3", "-c", cloudRunServerPy},
			Links:            []core.Resource{uploads},
			ExtraLabels:      map[string]string{"openbase-revision": revision},
			Env: map[string]pulumi.StringInput{
				"OPENBASE_ENVIRONMENT":           pulumi.String(env.OpenBaseMeta().Environment.String()),
				"OPENBASE_REVISION":              pulumi.String(revision),
				"STORAGE_BUCKET":                 uploads.BucketName,
				"STORAGE_FALLBACK_BUCKET":        uploads.FallbackBucketName,
				"KINDE_ISSUER_URL":               auth.IssuerURL,
				"KINDE_CLIENT_ID":                auth.ClientID,
				"KINDE_SITE_URL":                 auth.SiteURL,
				"KINDE_REDIRECT_URL":             auth.RedirectURL,
				"KINDE_POST_LOGIN_REDIRECT_URL":  auth.PostLoginRedirectURL,
				"KINDE_POST_LOGOUT_REDIRECT_URL": auth.PostLogoutRedirectURL,
			},
			Secrets: map[string]pulumi.StringInput{
				"DATABASE_URL":        db.DatabaseURL,
				"DIRECT_URL":          db.DirectURL,
				"KINDE_CLIENT_SECRET": auth.ClientSecret,
			},
		})
		return err
	})
}

func resolveCloudRunSiteURL(project string, tier core.Environment, projectNum, baseDomain string) (string, error) {
	if dom, err := core.EnvironmentDomain(project, tier, "web", baseDomain); err == nil && dom != "" {
		return "https://" + dom, nil
	}
	return gcp.CloudRunRegionalURL(project, tier, "web", projectNum, "us-central1", baseDomain)
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
