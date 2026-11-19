package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

const ordersPythonServer = `import http.server, json, os

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        payload = {
            "ok": True,
            "service": "orders",
            "storageBucket": os.environ.get("STORAGE_BUCKET", ""),
            "fallbackBucket": os.environ.get("STORAGE_FALLBACK_BUCKET", ""),
            "sharedAssetsBucket": os.environ.get("SHARED_ASSETS_BUCKET", ""),
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
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
		teamID := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_ID")
		teamSlug := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_SLUG")

		env, err := openbase.NewEnvironment(ctx, openbase.Environment{
			Project:          appProject,
			PlatformStackRef: "organization/platform/platform",
		})
		if err != nil {
			return err
		}
		uploads, err := openbase.Add(env, gcp.Bucket{
			Name:            "uploads",
			GCPProject:      pulumi.String(gcpProject),
			BucketNamespace: gcpProject,
		})
		if err != nil {
			return err
		}
		if !env.OpenBaseMeta().Environment.IsPreview() {
			return nil
		}
		return deployHybridPreview(ctx, env, appProject, gcpProject, teamID, teamSlug, uploads)
	})
}

func deployHybridPreview(
	ctx *pulumi.Context,
	env *openbase.EnvironmentResource,
	appProject, gcpProject, teamID, teamSlug string,
	uploads *gcp.BucketResource,
) error {
	plat := env.Platform()
	proj := env.Project()
	sharedAssets := proj.Resource("shared-assets", core.WithRemoteKind("gcp-bucket"))

	orders, err := openbase.Add(env, gcp.CloudRunService{
		Name:       "orders",
		GCPProject: pulumi.String(gcpProject),
		Links:      []core.Resource{uploads, sharedAssets},
		Env: map[string]pulumi.StringInput{
			"STORAGE_BUCKET":          uploads.BucketName,
			"STORAGE_FALLBACK_BUCKET": uploads.FallbackBucketName,
			"SHARED_ASSETS_BUCKET":    sharedAssets.Attr("bucketName"),
		},
		Image:      pulumi.String("mirror.gcr.io/library/python:3.12-slim"),
		Command:    []string{"python3", "-c", ordersPythonServer},
		Visibility: gcp.VisibilityInternal,
	})
	if err != nil {
		return err
	}
	fed, err := openbase.Add(env, gcp.FederatedIdentity{
		Name:           "web-wif",
		GCPProject:     pulumi.String(gcpProject),
		PoolName:       plat.Output("workloadIdentityPoolName"),
		OIDCProvider:   plat.Output("vercelOidcProviderName"),
		SourceProvider: "vercel",
		ClaimKey:       "project_id",
		ClaimValue:     proj.Output("vercelProjectId"),
	})
	if err != nil {
		return err
	}
	web, err := openbase.Add(env, vercel.WebApp{
		Name:              "web",
		VercelProjectName: "openbase-e2e-" + appProject,
		TeamID:            teamID,
		TeamSlug:          teamSlug,
		SourceDir:         "../site",
		Links:             []core.Resource{orders, uploads, sharedAssets},
		Brokers:           []core.IdentityBroker{fed},
		Env: map[string]pulumi.StringInput{
			"GCP_SERVICE_ACCOUNT_EMAIL":      fed.ServiceAccountEmail,
			"GCP_WORKLOAD_IDENTITY_PROVIDER": fed.ProviderAudience,
			"ORDERS_URL":                     orders.URL,
			"STORAGE_BUCKET":                 uploads.BucketName,
			"STORAGE_FALLBACK_BUCKET":        uploads.FallbackBucketName,
			"SHARED_ASSETS_BUCKET":           sharedAssets.Attr("bucketName"),
		},
	})
	if err != nil {
		return err
	}
	ctx.Export("webUrl", web.URL)
	ctx.Export("webDeploymentId", web.DeploymentID)
	ctx.Export("ordersUrl", orders.URL)
	ctx.Export("ordersServiceName", orders.Service.Name)
	return nil
}
