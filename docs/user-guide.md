# OpenBase user guide

OpenBase components use standard Pulumi Go constructors (`New<Resource>(ctx, name, &<Resource>Args{...}, opts...)`) across three Premise root programs:

1. `infra/platform/main.go` runs once per organization on stack `platform` (`gcp.NewPlatform`, `vercel.NewPlatform`, `neon.NewPlatform`, `kinde.NewPlatform`) to enable GCP APIs, create one Workload Identity Pool and wildcard HTTPS Load Balancer, export Vercel team and Neon organization defaults, and connect `NonProd` and `Prod` Kinde tenants.
2. `infra/shared/main.go` runs once per application repository on stack `shared` (`pm infra apply --shared`) to provision cross-environment resources (`gcp.NewProject`, `vercel.NewProject`, `neon.NewProject`, `kinde.NewProject`, or shared `gcp.NewBucket` stores).
3. `infra/env/main.go` runs on `dev`, `stage`, `prod`, and `preview-<slug>` stacks (`pm infra apply --env ...`) to provision isolated compute, copy-on-write Neon branches, storage overlays, OIDC bindings, and any native Pulumi resources directly on `ctx`.

## Stack topology

```mermaid
flowchart TD
    OrgProg["Platform Repo (infra/platform/main.go)"] --> OrgStack["Stack: acme/platform/platform"]
    ProjProg["App Shared Program (infra/shared/main.go)"] --> ProjStack["Stack: acme/storefront-shared/shared"]
    EnvProg["App Environment Program (infra/env/main.go)"] --> Dev["Stack: dev"]
    EnvProg --> Stage["Stack: stage (BranchFrom: prod)"]
    EnvProg --> Prod["Stack: prod"]
    EnvProg --> Preview["Stack: preview-<slug> (BranchFrom: dev)"]
    OrgStack -->|"openbase.LookupPlatform(ctx)"| ProjStack
    ProjStack -->|"openbase.LookupShared(ctx) + openbaseManifest"| Dev
    ProjStack -->|"openbase.LookupShared(ctx) + openbaseManifest"| Stage
    ProjStack -->|"openbase.LookupShared(ctx) + openbaseManifest"| Prod
    ProjStack -->|"openbase.LookupShared(ctx) + openbaseManifest"| Preview
```

## 1. Install OpenBase

```sh
go get github.com/cloudvoyant/openbase/libs/components@latest
```

## 2. Organization platform (`infra/platform/main.go`)

Deploy one organization platform stack into your shared Pulumi backend (`gs://<org>-pulumi-state`):

```go
package main

import (
	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")
		vercelPlat, err := vercel.NewPlatform(ctx, "vercel-platform", &vercel.PlatformArgs{
			Org:        "acme",
			TeamID:     "team_123",
			TeamSlug:   "acme",
			BaseDomain: "vercel.acme.io",
		})
		if err != nil {
			return err
		}
		if _, err := gcp.NewPlatform(ctx, "gcp-platform", &gcp.PlatformArgs{
			Org:                "acme",
			GCPProject:         pulumi.String("acme-cloud"),
			GCPProjectNumber:   "123456789012",
			Links:              []core.OIDCProviderSource{vercelPlat, core.GitHubActionsOIDC("acme")},
			BaseDomain:         "gcp.acme.io",
			EnableLoadBalancer: true,
		}); err != nil {
			return err
		}
		if _, err := neon.NewPlatform(ctx, "neon-platform", &neon.PlatformArgs{
			Org:             "acme",
			OrgID:           "org-acme",
			DefaultRegionID: "aws-us-east-1",
		}); err != nil {
			return err
		}
		_, err = kinde.NewPlatform(ctx, "kinde-platform", &kinde.PlatformArgs{
			Org: "acme",
			NonProd: kinde.TenantCredentials{
				Domain:       "acme-nonprod.kinde.com",
				ClientID:     cfg.Require("kindeNonProdClientId"),
				ClientSecret: cfg.RequireSecret("kindeNonProdClientSecret"),
			},
			Prod: kinde.TenantCredentials{
				Domain:       "acme.kinde.com",
				ClientID:     cfg.Require("kindeProdClientId"),
				ClientSecret: cfg.RequireSecret("kindeProdClientSecret"),
			},
		})
		return err
	})
}
```

## 3. Shared repository project (`infra/shared/main.go`)

Set `workspace.infra.directory: infra/env` and `workspace.infra.shared_directory: infra/shared` in `premise.yaml`. Call `openbase.LookupPlatform(ctx)` once and pass `PlatformStack` into each `<provider>.NewProject` constructor:

```go
package main

import (
	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		plat, err := openbase.LookupPlatform(ctx, "acme/platform/platform")
		if err != nil {
			return err
		}
		authProj, err := kinde.NewProject(ctx, "kinde-project", &kinde.ProjectArgs{
			PlatformStack:  plat,
			VercelTeamSlug: "acme",
			BaseDomain:     "vercel.acme.io",
		})
		if err != nil {
			return err
		}
		if _, err := neon.NewProject(ctx, "neon-project", &neon.ProjectArgs{
			PlatformStack: plat,
		}); err != nil {
			return err
		}
		_, err = vercel.NewProject(ctx, "vercel-project", &vercel.ProjectArgs{
			PlatformStack:   plat,
			EnableBlobStore: true,
			PreviewEnv: map[string]pulumi.StringInput{
				"KINDE_ISSUER_URL": authProj.NonProdIssuerURL,
				"KINDE_CLIENT_ID":  authProj.NonProdClientID,
			},
			PreviewSecrets: map[string]pulumi.StringInput{
				"KINDE_CLIENT_SECRET": authProj.NonProdClientSecret,
			},
		})
		return err
	})
}
```

## 4. Per-environment stack (`infra/env/main.go`)

`openbase.LookupShared(ctx)` resolves `<org>/<project>-shared/shared` automatically, and stateful constructors set default branching (`stage` from `prod`, `preview-*` from `dev`). `Links` grants IAM and OIDC access, while callers wire their own `Env` and `Secrets` keys directly from typed resource outputs:

```go
package main

import (
	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/kinde"
	"github.com/cloudvoyant/openbase/libs/components/neon"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")
		shared, err := openbase.LookupShared(ctx)
		if err != nil {
			return err
		}
		db, err := neon.NewDatabase(ctx, "db", &neon.DatabaseArgs{
			Shared: shared,
			APIKey: cfg.RequireSecret("neonApiKey"),
		})
		if err != nil {
			return err
		}
		uploads, err := vercel.NewBlobStore(ctx, "uploads", &vercel.BlobStoreArgs{
			Shared:             shared,
			TeamID:             "team_123",
			BlobReadWriteToken: cfg.RequireSecret("blobReadWriteToken"),
		})
		if err != nil {
			return err
		}
		auth, err := kinde.NewOidcApp(ctx, "auth", &kinde.OidcAppArgs{
			Shared:         shared,
			VercelTeamSlug: "acme",
			BaseDomain:     "vercel.acme.io",
		})
		if err != nil {
			return err
		}
		_, err = vercel.NewWebApp(ctx, "web", &vercel.WebAppArgs{
			Shared:     shared,
			TeamID:     "team_123",
			TeamSlug:   "acme",
			BaseDomain: "vercel.acme.io",
			SourceDir:  "../../apps/web",
			Links:      []core.Resource{uploads},
			Env: map[string]pulumi.StringInput{
				"BLOB_BASE_URL":        uploads.BlobBaseURL,
				"BLOB_PREFIX":          pulumi.String(uploads.BlobPrefix),
				"BLOB_FALLBACK_PREFIX": pulumi.String(uploads.BlobFallbackPrefix),
				"KINDE_ISSUER_URL":     auth.IssuerURL,
				"KINDE_CLIENT_ID":      auth.ClientID,
				"KINDE_REDIRECT_URL":   auth.RedirectURL,
			},
			Secrets: map[string]pulumi.StringInput{
				"DATABASE_URL":          db.DatabaseURL,
				"DIRECT_URL":            db.DirectURL,
				"BLOB_READ_WRITE_TOKEN": cfg.RequireSecret("blobReadWriteToken"),
				"KINDE_CLIENT_SECRET":   auth.ClientSecret,
			},
		})
		return err
	})
}
```

## 5. Hybrid multi-app and cross-cloud WIF

An `infra/env/main.go` program can deploy multiple apps and services (such as a `vercel.NewWebApp` frontend and an internal `gcp.NewCloudRunService` API) and link shared resources from `infra/shared`. Call `gcp.NewFederatedIdentity` (or pass `gcp.WIFBroker`) so external OIDC callers receive federated Google Cloud service accounts at the `Links` boundary:

```go
plat, err := openbase.LookupPlatform(ctx, "acme/platform/platform")
if err != nil {
	return err
}
shared, err := openbase.LookupShared(ctx)
if err != nil {
	return err
}
sharedAssets := shared.Resource(
	"shared-assets",
	core.WithRemoteKind("gcp-bucket"),
)
fed, err := gcp.NewFederatedIdentity(ctx, "web-wif", &gcp.FederatedIdentityArgs{
	GCPProject:     pulumi.String("acme-cloud"),
	PoolName:       plat.Output("workloadIdentityPoolName"),
	OIDCProvider:   plat.Output("vercelOidcProviderName"),
	SourceProvider: "vercel",
	ClaimKey:       "project_id",
	ClaimValue:     shared.Output("vercelProjectId"),
})
if err != nil {
	return err
}
ordersAPI, err := gcp.NewCloudRunService(ctx, "orders", &gcp.CloudRunServiceArgs{
	GCPProject: pulumi.String("acme-cloud"),
	Visibility: gcp.VisibilityInternal,
	Image:      pulumi.String("us-central1-docker.pkg.dev/acme-cloud/storefront-gcp/orders:latest"),
	Links:      []core.Resource{sharedAssets},
	Env: map[string]pulumi.StringInput{
		"ASSETS_BUCKET": sharedAssets.Attr("bucketName"),
	},
	Secrets: map[string]pulumi.StringInput{
		"DATABASE_URL": db.DatabaseURL,
	},
})
if err != nil {
	return err
}
_, err = vercel.NewWebApp(ctx, "web", &vercel.WebAppArgs{
	Shared:    shared,
	SourceDir: "../../apps/web",
	Links:     []core.Resource{ordersAPI, sharedAssets},
	Brokers:   []core.IdentityBroker{fed},
	Env: map[string]pulumi.StringInput{
		"GCP_SERVICE_ACCOUNT_EMAIL":      fed.ServiceAccountEmail,
		"GCP_WORKLOAD_IDENTITY_PROVIDER": fed.ProviderAudience,
		"ORDERS_URL":                     ordersAPI.URL,
		"ASSETS_BUCKET":                  sharedAssets.Attr("bucketName"),
	},
})
```

## 6. Mixing raw Pulumi resources and multiple cloud accounts

OpenBase components register directly on `*pulumi.Context` with the same `New<Resource>(ctx, name, &<Resource>Args{...}, opts...)` signature as native Pulumi SDK resources. You can declare native Pulumi resources alongside OpenBase components and wire outputs in both directions:

```go
// 1. Raw Pulumi resource created directly on ctx.
rawSecret, err := secretmanager.NewSecret(ctx, "raw-partner-key", &secretmanager.SecretArgs{
	Project:     pulumi.String("acme-cloud"),
	SecretId:    pulumi.String("storefront-" + ctx.Stack() + "-partner-key"),
	Replication: &secretmanager.SecretReplicationArgs{Auto: &secretmanager.SecretReplicationAutoArgs{}},
})
if err != nil {
	return err
}
rawSecretVer, err := secretmanager.NewSecretVersion(ctx, "raw-partner-key-v1", &secretmanager.SecretVersionArgs{
	Secret:     rawSecret.ID(),
	SecretData: cfg.RequireSecret("partnerApiKey"),
})
if err != nil {
	return err
}

// 2. Pass raw Pulumi outputs into an OpenBase resource.
api, err := gcp.NewCloudRunService(ctx, "orders", &gcp.CloudRunServiceArgs{
	GCPProject: pulumi.String("acme-cloud"),
	Image:      pulumi.String("us-central1-docker.pkg.dev/acme-cloud/storefront-gcp/orders:latest"),
	Env: map[string]pulumi.StringInput{
		"RAW_SECRET_VERSION": rawSecretVer.Name,
	},
})
if err != nil {
	return err
}

// 3. Pass typed OpenBase outputs (such as ServiceAccountEmail) into raw Pulumi IAM resources.
_, err = secretmanager.NewSecretIamMember(ctx, "raw-partner-key-accessor", &secretmanager.SecretIamMemberArgs{
	Project:  pulumi.String("acme-cloud"),
	SecretId: rawSecret.SecretId,
	Role:     pulumi.String("roles/secretmanager.secretAccessor"),
	Member:   pulumi.Sprintf("serviceAccount:%s", api.ServiceAccountEmail),
})
```

When an organization spans multiple Google Cloud projects or multiple cloud accounts, pass standard `pulumi.Provider(...)` options directly to `NewPlatform`, `NewProject`, or environment constructors:

```go
// infra/platform/main.go: multiple GCP projects or accounts
analyticsGCP, err := gcpprov.NewProvider(ctx, "gcp-analytics", &gcpprov.ProviderArgs{
	Project: pulumi.String("acme-analytics"),
})
_, err = gcp.NewPlatform(ctx, "gcp-core", &gcp.PlatformArgs{
	Org:        "acme",
	GCPProject: pulumi.String("acme-cloud"),
})
_, err = gcp.NewPlatform(ctx, "gcp-analytics", &gcp.PlatformArgs{
	Org:        "acme",
	GCPProject: pulumi.String("acme-analytics"),
}, pulumi.Provider(analyticsGCP))

// infra/shared/main.go: target a specific named platform entry via PlatformResource
_, err = gcp.NewProject(ctx, "ar-analytics", &gcp.ProjectArgs{
	PlatformStack:    plat,
	PlatformResource: "gcp-analytics",
}, pulumi.Provider(analyticsGCP))

// infra/env/main.go: target multiple GCP projects or accounts in one environment
_, err = gcp.NewBucket(ctx, "analytics-events", &gcp.BucketArgs{
	GCPProject: pulumi.String("acme-analytics"),
}, pulumi.Provider(analyticsGCP))
```

## 7. Zero-copy storage reads in Go

1. For Google Cloud Storage (`libs/components/gcp/overlayfs`), call `overlayfs.NewStore(storageClient, primaryBucket, fallbackBucket)` so `preview-*` reads fall back to `dev` on `storage.ErrObjectNotExist` while writes stay isolated in the preview bucket.
2. For Vercel Blob (`libs/components/vercel`), call `vercel.NewBlobOverlayClient(baseURL, token, primaryPrefix, fallbackPrefix)` for prefix-isolated writes and fallback reads.

## Further reading

- [Architecture Overview](./architecture/index.md)
- [Cross-Stack Lookups and Manifests](./architecture/scopes.md)
- [Core Resources and Tier Contracts](./architecture/resources.md)
- [GCP Provider](./architecture/providers/gcp/index.md)
- [Vercel Provider](./architecture/providers/vercel/index.md)
- [Neon Provider](./architecture/providers/neon/index.md)
- [Kinde Provider](./architecture/providers/kinde/index.md)
