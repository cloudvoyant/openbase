package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
)

//go:embed app/server.py
var cloudRunServerPy string

const defaultRegion = "us-central1"

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
	appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obgapp")
	gcpProject := envOrDefault("OPENBASE_GCP_PROJECT", "OPENBASE_TEST_GCP_PROJECT", "cloudvoyant-openbase")
	projectNum := envOrDefault("OPENBASE_GCP_PROJECT_NUMBER", "OPENBASE_TEST_GCP_PROJECT_NUMBER", "785485436615")

	gcpShared, err := gcp.Shared{
		Name:        "gcp",
		Project:     appProject,
		GCPProject:  pulumi.String(gcpProject),
		Location:    defaultRegion,
		PoolSuffix:  os.Getenv("OPENBASE_TEST_RUN_ID"),
		SkipKeyRing: true,
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
	kindeShared, err := registerSharedKinde(ctx, appProject, projectNum)
	if err != nil {
		return err
	}
	exportShared(ctx, gcpShared, neonShared, kindeShared)
	return nil
}

func registerSharedKinde(ctx *pulumi.Context, appProject, projectNum string) (*kinde.SharedOidcAppResource, error) {
	stageURL, err := gcp.CloudRunURL(appProject, core.EnvStage, "web", projectNum, defaultRegion)
	if err != nil {
		return nil, err
	}
	prodURL, err := gcp.CloudRunURL(appProject, core.EnvProd, "web", projectNum, defaultRegion)
	if err != nil {
		return nil, err
	}
	appName := envOrDefault("OPENBASE_KINDE_APP_NAME", "OPENBASE_TEST_KINDE_APP_NAME", "openbase-e2e-"+appProject)
	importDev := envOrDefault("OPENBASE_IMPORT_DEV_APP_ID", "OPENBASE_TEST_IMPORT_DEV_APP_ID", "")
	importProd := envOrDefault("OPENBASE_IMPORT_PROD_APP_ID", "OPENBASE_TEST_IMPORT_PROD_APP_ID", "")
	retain := os.Getenv("OPENBASE_RETAIN_KINDE_APPS") == "1" || os.Getenv("OPENBASE_TEST_RETAIN_KINDE_APPS") == "1"
	regWildcard := fmt.Sprintf("https://%s-*-%s.%s.run.app", appProject, projectNum, defaultRegion)
	hashWildcard := fmt.Sprintf("https://%s-*-uc.a.run.app", appProject)

	return kinde.SharedOidcApp{
		Name:                       "kinde",
		Project:                    appProject,
		AppName:                    appName,
		StageDomain:                strings.TrimPrefix(stageURL, "https://"),
		ProdDomain:                 strings.TrimPrefix(prodURL, "https://"),
		ExtraDevRedirectURIs:       []string{regWildcard + "/api/auth/kinde_callback", hashWildcard + "/api/auth/kinde_callback"},
		ExtraDevLogoutURIs:         []string{regWildcard + "/", hashWildcard + "/"},
		ImportDevAppID:             importDev,
		ImportProdAppID:            importProd,
		RetainApplicationsOnDelete: retain,
		Dev:                        kindeCreds("KINDE_DEV_TENANT_DOMAIN", "KINDE_DEV_M2M_CLIENT_ID", "KINDE_DEV_M2M_CLIENT_SECRET"),
		Prod:                       kindeCreds("KINDE_PROD_TENANT_DOMAIN", "KINDE_PROD_M2M_CLIENT_ID", "KINDE_PROD_M2M_CLIENT_SECRET"),
	}.Register(ctx)
}

func kindeCreds(domainKey, clientIDKey, clientSecretKey string) kinde.TenantCredentials {
	return kinde.TenantCredentials{
		Domain:       os.Getenv(domainKey),
		ClientID:     pulumi.String(os.Getenv(clientIDKey)),
		ClientSecret: pulumi.String(os.Getenv(clientSecretKey)),
	}
}

func exportShared(
	ctx *pulumi.Context,
	g *gcp.SharedResource,
	n *neon.SharedProjectResource,
	k *kinde.SharedOidcAppResource,
) {
	ctx.Export("repositoryId", g.RepositoryID)
	ctx.Export("repositoryUrl", g.RepositoryURL)
	ctx.Export("workloadIdentityPoolName", g.WorkloadIdentityPoolName)
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
	appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obgapp")
	gcpProject := envOrDefault("OPENBASE_GCP_PROJECT", "OPENBASE_TEST_GCP_PROJECT", "cloudvoyant-openbase")
	projectNum := envOrDefault("OPENBASE_GCP_PROJECT_NUMBER", "OPENBASE_TEST_GCP_PROJECT_NUMBER", "785485436615")
	sharedRef := fmt.Sprintf("%s/%s/shared", ctx.Organization(), ctx.Project())
	regionalURL, err := gcp.CloudRunURL(appProject, env, "web", projectNum, defaultRegion)
	if err != nil {
		return err
	}

	uploads, db, auth, err := registerBackingResources(ctx, env, appProject, gcpProject, sharedRef, regionalURL)
	if err != nil {
		return err
	}
	web, err := gcp.CloudRunService{
		Name:             "web",
		Project:          appProject,
		Links:            []core.Resource{uploads, db, auth},
		Env:              map[string]pulumi.StringInput{"OPENBASE_ENVIRONMENT": pulumi.String(env.String())},
		GCPProject:       pulumi.String(gcpProject),
		GCPProjectNumber: pulumi.String(projectNum),
		SharedStackRef:   sharedRef,
		Location:         defaultRegion,
		Image:            pulumi.String("mirror.gcr.io/library/python:3.12-slim"),
		Command:          []string{"python3", "-c", cloudRunServerPy},
		Visibility:       gcp.VisibilityPublic,
	}.Register(ctx)
	if err != nil {
		return err
	}
	exportEnvironment(ctx, web, uploads, db, auth, regionalURL)
	return nil
}

func registerBackingResources(
	ctx *pulumi.Context,
	env core.Environment,
	appProject, gcpProject, sharedRef, regionalURL string,
) (*gcp.BucketResource, *neon.DatabaseResource, *kinde.OidcAppResource, error) {
	dbBranch, dbBranchRef := resolveDBBranch(ctx, env)
	bucketBranch, bucketBranchRef := resolveBucketBranch(ctx, env)

	uploads, err := gcp.Bucket{
		Name:            "uploads",
		Project:         appProject,
		BranchFrom:      bucketBranch,
		BranchStackRef:  bucketBranchRef,
		GCPProject:      pulumi.String(gcpProject),
		BucketNamespace: gcpProject,
	}.Register(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	db, err := neon.Database{
		Name:           "db",
		Project:        appProject,
		BranchFrom:     dbBranch,
		SharedStackRef: sharedRef,
		BranchStackRef: dbBranchRef,
	}.Register(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	auth, err := kinde.OidcApp{
		Name:           "auth",
		Project:        appProject,
		SharedStackRef: sharedRef,
		SiteURL:        pulumi.String(regionalURL),
	}.Register(ctx)
	return uploads, db, auth, err
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

func resolveBucketBranch(ctx *pulumi.Context, env core.Environment) (string, string) {
	if env.IsPreview() {
		return "dev", fmt.Sprintf("%s/%s/dev", ctx.Organization(), ctx.Project())
	}
	return "", ""
}

func exportEnvironment(
	ctx *pulumi.Context,
	web *gcp.CloudRunServiceResource,
	uploads *gcp.BucketResource,
	db *neon.DatabaseResource,
	auth *kinde.OidcAppResource,
	regionalURL string,
) {
	ctx.Export("url", web.URL)
	ctx.Export("regionalUrl", pulumi.String(regionalURL))
	ctx.Export("serviceName", web.Service.Name)
	ctx.Export("serviceAccountEmail", web.ServiceAccountEmail)
	ctx.Export("bucketName", uploads.BucketName)
	ctx.Export("fallbackBucketName", uploads.FallbackBucketName)
	ctx.Export("branchId", db.BranchID)
	ctx.Export("databaseUrl", db.DatabaseURL)
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
