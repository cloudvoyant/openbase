package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

const ordersPythonServer = `import hashlib, http.server, json, os

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        payload = {
            "ok": True,
            "service": "orders",
            "hasDatabaseUrl": "-pooler" in os.environ.get("DATABASE_URL", ""),
            "kindeIssuer": os.environ.get("KINDE_ISSUER_URL", ""),
            "kindeSecretSha256": hashlib.sha256(os.environ.get("KINDE_CLIENT_SECRET", "").encode()).hexdigest(),
            "storageBucket": os.environ.get("STORAGE_BUCKET", ""),
            "fallbackBucket": os.environ.get("STORAGE_FALLBACK_BUCKET", ""),
        }
        raw = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)
    def log_message(self, fmt, *args):
        return

http.server.ThreadingHTTPServer(("0.0.0.0", int(os.environ.get("PORT", "8080"))), Handler).serve_forever()
`

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		env, err := core.ResolveEnvironment(ctx, "")
		if err != nil {
			return err
		}
		if env == core.EnvShared {
			return deploySharedFullstack(ctx)
		}
		return deployAppStack(ctx, env)
	})
}

func deploySharedFullstack(ctx *pulumi.Context) error {
	appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
	gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
	teamID := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_ID")
	teamSlug := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_SLUG")

	gcpShared, err := gcp.Shared{
		Name:           "gcp-shared",
		Project:        appProject,
		GCPProject:     pulumi.String(gcpProject),
		PoolSuffix:     os.Getenv("OPENBASE_TEST_RUN_ID"),
		VercelTeamSlug: teamSlug,
		SkipKeyRing:    true,
	}.Register(ctx)
	if err != nil {
		return err
	}
	vercelShared, err := vercel.Shared{
		Name:                 "vercel-shared",
		Project:              appProject,
		VercelProjectName:    "openbase-e2e-" + appProject,
		TeamID:               teamID,
		TeamSlug:             teamSlug,
		SkipStageEnvironment: true,
	}.Register(ctx)
	if err != nil {
		return err
	}
	neonShared, err := neon.SharedProject{
		Name:     "neon-shared",
		Project:  appProject,
		OrgID:    os.Getenv("NEON_ORG_ID"),
		RegionID: "aws-us-east-1",
	}.Register(ctx)
	if err != nil {
		return err
	}
	kindeShared, err := createSharedKindeOidc(ctx, appProject, teamSlug)
	if err != nil {
		return err
	}
	exportSharedOutputs(ctx, gcpShared, vercelShared, neonShared, kindeShared)
	return nil
}

func createSharedKindeOidc(ctx *pulumi.Context, appProject, teamSlug string) (*kinde.SharedOidcAppResource, error) {
	return kinde.SharedOidcApp{
		Name:                       "kinde-shared",
		Project:                    appProject,
		AppName:                    os.Getenv("OPENBASE_TEST_KINDE_APP_NAME"),
		VercelProjectName:          "openbase-e2e-" + appProject,
		VercelTeamSlug:             teamSlug,
		ImportDevAppID:             os.Getenv("OPENBASE_TEST_IMPORT_DEV_APP_ID"),
		ImportProdAppID:            os.Getenv("OPENBASE_TEST_IMPORT_PROD_APP_ID"),
		RetainApplicationsOnDelete: true,
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

func exportSharedOutputs(
	ctx *pulumi.Context,
	g *gcp.SharedResource,
	v *vercel.SharedResource,
	n *neon.SharedProjectResource,
	k *kinde.SharedOidcAppResource,
) {
	ctx.Export("wifPoolName", g.WorkloadIdentityPoolName)
	ctx.Export("vercelOidcProviderName", g.VercelOIDCProviderName)
	ctx.Export("vercelProjectId", v.ProjectID)
	ctx.Export("protectionBypassSecret", v.ProtectionBypassSecret)
	ctx.Export("neonProjectId", n.ProjectID)
	ctx.Export("neonDefaultBranchId", n.DefaultBranchID)
	ctx.Export("devIssuerUrl", k.DevIssuerURL)
	ctx.Export("devClientId", k.DevClientID)
	ctx.Export("devClientSecret", k.DevClientSecret)
}

func deployAppStack(ctx *pulumi.Context, env core.Environment) error {
	appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
	gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
	branchFrom := ""
	if env.IsPreview() {
		branchFrom = "dev"
	}
	uploads, db, err := deployStatefulLayer(ctx, appProject, gcpProject, branchFrom)
	if err != nil {
		return err
	}
	if !env.IsPreview() {
		ctx.Export("bucketName", uploads.BucketName)
		ctx.Export("branchId", db.BranchID)
		ctx.Export("databaseUrl", db.DatabaseURL)
		return nil
	}
	return deployPreviewCompute(ctx, appProject, gcpProject, uploads, db)
}

func deployStatefulLayer(
	ctx *pulumi.Context,
	appProject, gcpProject, branchFrom string,
) (*gcp.BucketResource, *neon.DatabaseResource, error) {
	uploads, err := gcp.Bucket{
		Name:            "uploads",
		Project:         appProject,
		BranchFrom:      branchFrom,
		GCPProject:      pulumi.String(gcpProject),
		BucketNamespace: gcpProject,
	}.Register(ctx)
	if err != nil {
		return nil, nil, err
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
	return uploads, db, err
}

func deployPreviewCompute(
	ctx *pulumi.Context,
	appProject, gcpProject string,
	uploads *gcp.BucketResource,
	db *neon.DatabaseResource,
) error {
	teamID := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_ID")
	teamSlug := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_SLUG")
	vProjName := "openbase-e2e-" + appProject

	oidc, err := kinde.OidcApp{
		Name:              "auth",
		Project:           appProject,
		IssuerURL:         pulumi.String(os.Getenv("OPENBASE_TEST_DEV_ISSUER")),
		ClientID:          pulumi.String(os.Getenv("OPENBASE_TEST_DEV_CLIENT_ID")),
		ClientSecret:      pulumi.String(os.Getenv("OPENBASE_TEST_DEV_CLIENT_SECRET")),
		VercelProjectName: vProjName,
		VercelTeamSlug:    teamSlug,
	}.Register(ctx)
	if err != nil {
		return err
	}
	orders, err := gcp.CloudRunService{
		Name:       "orders",
		Project:    appProject,
		Links:      []core.Resource{uploads, db, oidc},
		GCPProject: pulumi.String(gcpProject),
		Image:      pulumi.String("mirror.gcr.io/library/python:3.12-slim"),
		Command:    []string{"python3", "-c", ordersPythonServer},
		Visibility: gcp.VisibilityInternal,
	}.Register(ctx)
	if err != nil {
		return err
	}
	web, err := vercel.WebApp{
		Name:                    "web",
		Project:                 appProject,
		Links:                   []core.Resource{orders, uploads, db, oidc},
		VercelProjectID:         pulumi.String(os.Getenv("OPENBASE_TEST_VERCEL_PROJECT_ID")),
		VercelProjectName:       vProjName,
		TeamID:                  teamID,
		TeamSlug:                teamSlug,
		SourceDir:               "site",
		PathPrefix:              "site",
		GCPProject:              pulumi.String(gcpProject),
		GCPWorkloadIdentityPool: pulumi.String(os.Getenv("OPENBASE_TEST_WIF_POOL_NAME")),
		GCPVercelOIDCProvider:   pulumi.String(os.Getenv("OPENBASE_TEST_VERCEL_OIDC_PROVIDER")),
	}.Register(ctx)
	if err != nil {
		return err
	}
	ctx.Export("webUrl", web.URL)
	ctx.Export("webDeploymentId", web.DeploymentID)
	ctx.Export("ordersUrl", orders.URL)
	ctx.Export("ordersServiceName", orders.Service.Name)
	ctx.Export("bucketName", uploads.BucketName)
	ctx.Export("branchId", db.BranchID)
	ctx.Export("databaseUrl", db.DatabaseURL)
	return nil
}
