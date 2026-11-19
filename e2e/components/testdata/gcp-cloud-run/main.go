package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
)

const probePythonScript = `import base64, datetime, hashlib, http.server, json, os, urllib.parse, urllib.request

def meta(path):
    req = urllib.request.Request(
        "http://metadata.google.internal/computeMetadata/v1/" + path,
        headers={"Metadata-Flavor": "Google"},
    )
    with urllib.request.urlopen(req, timeout=10) as r:
        return r.read().decode()

def sign_gcs_v4(bucket, obj):
    sa_email = meta("instance/service-accounts/default/email").strip()
    tok = json.loads(meta("instance/service-accounts/default/token"))["access_token"]
    now = datetime.datetime.now(datetime.timezone.utc)
    datestamp = now.strftime("%Y%m%d")
    timestamp = now.strftime("%Y%m%dT%H%M%SZ")
    scope = f"{datestamp}/auto/storage/goog4_request"
    cred = f"{sa_email}/{scope}"
    params = [
        ("X-Goog-Algorithm", "GOOG4-RSA-SHA256"),
        ("X-Goog-Credential", urllib.parse.quote(cred, safe="")),
        ("X-Goog-Date", timestamp),
        ("X-Goog-Expires", "300"),
        ("X-Goog-SignedHeaders", "host"),
    ]
    query = "&".join(f"{k}={v}" for k, v in params)
    escaped_obj = "/".join(urllib.parse.quote(p, safe="") for p in obj.split("/"))
    canonical = "\n".join([
        "GET",
        f"/{bucket}/{escaped_obj}",
        query,
        "host:storage.googleapis.com",
        "",
        "host",
        "UNSIGNED-PAYLOAD",
    ])
    sts = "\n".join([
        "GOOG4-RSA-SHA256",
        timestamp,
        scope,
        hashlib.sha256(canonical.encode()).hexdigest(),
    ])
    body = json.dumps({"payload": base64.b64encode(sts.encode()).decode()}).encode()
    sign_req = urllib.request.Request(
        f"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/{sa_email}:signBlob",
        data=body,
        headers={"Authorization": f"Bearer {tok}", "Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(sign_req, timeout=10) as r:
        sig_b64 = json.loads(r.read().decode())["signedBlob"]
    sig_hex = base64.b64decode(sig_b64).hex()
    return f"https://storage.googleapis.com/{bucket}/{escaped_obj}?{query}&X-Goog-Signature={sig_hex}"

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path == "/sign":
            qs = urllib.parse.parse_qs(parsed.query)
            obj = qs.get("object", ["probe.txt"])[0]
            url = sign_gcs_v4(os.environ["STORAGE_BUCKET"], obj)
            self.respond(200, {"signedUrl": url})
            return
        if parsed.path == "/call-internal":
            target = os.environ["INTERNAL_URL"]
            id_tok = meta(f"instance/service-accounts/default/identity?audience={urllib.parse.quote(target, safe='')}")
            req = urllib.request.Request(target + "/health", headers={"Authorization": f"Bearer {id_tok}"})
            with urllib.request.urlopen(req, timeout=15) as r:
                upstream = json.loads(r.read().decode())
            self.respond(200, {"upstream": upstream})
            return
        secret_val = os.environ.get("LINKED_SECRET", "")
        self.respond(200, {
            "ok": True,
            "revision": os.environ.get("OPENBASE_REVISION", ""),
            "storageBucket": os.environ.get("STORAGE_BUCKET", ""),
            "secretSha256": hashlib.sha256(secret_val.encode()).hexdigest(),
        })
    def respond(self, code, payload):
        raw = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)
    def log_message(self, fmt, *args):
        return

port = int(os.environ.get("PORT", "8080"))
http.server.ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
`

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		if core.IsProjectStack(ctx) {
			return deployProject(ctx)
		}
		env, err := core.ResolveEnvironment(ctx, "")
		if err != nil {
			return err
		}
		return deployEnvironment(ctx, env)
	})
}

func deployProject(ctx *pulumi.Context) error {
	gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
	appProject := defaultEnv("OPENBASE_TEST_APP_PROJECT", "obe2ecr")
	plat, err := gcp.Platform{
		Org:            "cloudvoyant",
		GCPProject:     pulumi.String(gcpProject),
		PoolSuffix:     os.Getenv("OPENBASE_TEST_RUN_ID"),
		GitHubOrg:      "cloudvoyant",
		VercelTeamSlug: "cloudvoyant",
	}.Register(ctx)
	if err != nil {
		return err
	}
	proj, err := gcp.Project{
		Project:    appProject,
		Repo:       "cloudvoyant/openbase",
		Platform:   plat,
		GCPProject: pulumi.String(gcpProject),
	}.Register(ctx)
	if err != nil {
		return err
	}
	ctx.Export("repositoryId", proj.RepositoryID)
	ctx.Export("workloadIdentityPoolName", proj.WorkloadIdentityPoolName)
	return nil
}

func deployEnvironment(ctx *pulumi.Context, env core.Environment) error {
	gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
	projectNum := os.Getenv("OPENBASE_TEST_GCP_PROJECT_NUMBER")
	appProject := defaultEnv("OPENBASE_TEST_APP_PROJECT", "obe2ecr")
	revision := defaultEnv("OPENBASE_TEST_REVISION", "a")
	secretSeed := defaultEnv("OPENBASE_TEST_SECRET_VALUE", "default-secret")
	repoID := os.Getenv("OPENBASE_TEST_AR_REPO_ID")

	bucket, err := gcp.Bucket{
		Name:            "assets",
		Project:         appProject,
		GCPProject:      pulumi.String(gcpProject),
		BucketNamespace: gcpProject,
		ForceDestroy:    boolPtr(true),
	}.Register(ctx)
	if err != nil {
		return err
	}
	secretCfg, err := core.Stateless{
		Name:    "cfg",
		Project: appProject,
		Secrets: map[string]pulumi.StringInput{"LINKED_SECRET": pulumi.String(secretSeed)},
	}.Register(ctx)
	if err != nil {
		return err
	}
	internalSvc, err := gcp.CloudRunService{
		Name:                 "internal",
		Project:              appProject,
		Env:                  map[string]pulumi.StringInput{"OPENBASE_REVISION": pulumi.String(revision)},
		GCPProject:           pulumi.String(gcpProject),
		GCPProjectNumber:     optionalStringInput(projectNum),
		ArtifactRegistryRepo: optionalStringInput(repoID),
		Image:                pulumi.String("mirror.gcr.io/library/python:3.12-slim"),
		Command:              []string{"python3", "-c", probePythonScript},
		Visibility:           gcp.VisibilityInternal,
		ExtraLabels:          map[string]string{"openbase-revision": revision},
	}.Register(ctx)
	if err != nil {
		return err
	}
	publicSvc, err := gcp.CloudRunService{
		Name:                 "public-gateway",
		Project:              appProject,
		Links:                []core.Resource{bucket, secretCfg, internalSvc},
		Env:                  map[string]pulumi.StringInput{"OPENBASE_REVISION": pulumi.String(revision)},
		GCPProject:           pulumi.String(gcpProject),
		GCPProjectNumber:     optionalStringInput(projectNum),
		ArtifactRegistryRepo: optionalStringInput(repoID),
		Image:                pulumi.String("mirror.gcr.io/library/python:3.12-slim"),
		Command:              []string{"python3", "-c", probePythonScript},
		Visibility:           gcp.VisibilityPublic,
		ExtraLabels:          map[string]string{"openbase-revision": revision},
	}.Register(ctx)
	if err != nil {
		return err
	}
	_ = env
	ctx.Export("publicUrl", publicSvc.URL)
	ctx.Export("publicServiceName", publicSvc.Service.Name)
	ctx.Export("publicSAId", pulumi.String(publicSvc.ServiceAccountID))
	ctx.Export("publicSAEmail", publicSvc.ServiceAccountEmail)
	ctx.Export("internalUrl", internalSvc.URL)
	ctx.Export("internalServiceName", internalSvc.Service.Name)
	ctx.Export("bucketName", bucket.BucketName)
	return nil
}

func defaultEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func optionalStringInput(val string) pulumi.StringInput {
	if val == "" {
		return nil
	}
	return pulumi.String(val)
}

func boolPtr(v bool) *bool { return &v }
