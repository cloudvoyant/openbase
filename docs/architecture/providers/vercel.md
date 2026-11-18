# Vercel provider - software architecture

## Overview

The `libs/components/vercel` package implements OpenBase's declarative Vercel components over `github.com/pulumiverse/pulumi-vercel/sdk/v5`. It provides `vercel.Shared` for project-level settings, custom `staging` environments, domains, and deployment-protection bypass tokens; `vercel.BlobStore` (`*vercel.BlobStoreResource`, `core.StatefulResource`) for prefix-isolated Blob storage with custom-environment token propagation; and `vercel.WebApp` (`*vercel.WebAppResource`, `core.StatelessResource`) for deployments, squat-proof team aliases, scoped environment variables, and keyless GCP OIDC federation.

## Requirements

### Functional

- Provision `vercel.Shared` once in the `shared` stack: `vercel.Project` (team OIDC issuer mode, `standard_protection_new` deployment protection, `IgnoreCommand: "exit 0"`), `vercel.CustomEnvironment` (`staging`), team-scoped `<project>-<teamSlug>.vercel.app` domains, `vercel.ProjectProtectionBypass`, and optional shared `vercel.BlobStore` + `BlobProjectConnection`.
- Provision `vercel.BlobStore` across `dev`, `stage`, `prod`, and `preview-*` stacks, deriving `BLOB_BASE_URL`, propagating `BLOB_READ_WRITE_TOKEN` into the `staging` `CustomEnvironment`, and exporting `BLOB_PREFIX=<env>/` plus `BLOB_FALLBACK_PREFIX=<branchFrom>/`.
- Deploy `vercel.WebApp` with deterministic `<project>-<slug>-<teamSlug>.vercel.app` preview aliases, target-scoped `ProjectEnvironmentVariable` bindings, and keyless GCP Workload Identity Federation.

### Non-Functional

- Never store static GCP service-account JSON keys in Vercel environment variables; exchange `x-vercel-oidc-token` via GCP STS and `iamcredentials` at runtime.
- Delete ephemeral preview `Deployment` and `Alias` resources automatically on `pulumi destroy`.

## Design

### Components and Responsibilities

```mermaid
flowchart TD
    Shared["vercel.Shared (shared stack)"] --> Proj["Project (team OIDC + ProtectionBypass)"]
    Shared --> StageEnv["CustomEnvironment (staging) + ProjectDomains"]
    Shared --> BlobRoot["BlobStore + BlobProjectConnection"]
    Env["Environment Stack (dev | stage | prod | preview-*)"] --> App["vercel.WebApp (StatelessResource)"]
    Env --> Blob["vercel.BlobStore (StatefulResource + BlobOverlayClient)"]
    Proj --> App
    StageEnv --> App
    BlobRoot --> Blob
    App -->|"Keyless OIDC WIF"| GCP["gcp.CloudRunService & gcp.Bucket"]
```

- **`vercel.Shared`**: Configures `OidcTokenConfig` (`IssuerMode: "team"` -> `https://oidc.vercel.com/<teamSlug>`), `IgnoreCommand: "exit 0"` (so Git pushes never auto-promote to `Production`; `dev` is the default merge publishing target via Premise while `stage` and `prod` require explicit promotion), `ProjectProtectionBypass` (`x-vercel-protection-bypass`), and flexible Custom Environment + Domain tiers:
  - **Custom Environments by plan quota**:
    - **Pro default (`1` slot)**: Built-in `Production` (`prod`) + `staging` `CustomEnvironment` (`stage`), while `dev` writes to built-in `development` (for local `vercel env pull`) and deploys with `{project}-dev...` alias.
    - **2+ Custom Environment plans (`EnableDevEnvironment: true`)**: Also creates a named `dev` `CustomEnvironment` (`devCustomEnvironmentId`) with its own attached `ProjectDomain` and scoped variables.
    - **Hobby / zero-slot plans (`SkipStageEnvironment: true`)**: Creates `0` `CustomEnvironment`s; `stage` deploys via `DeploymentArgs.Environment` + `{project}-stage...` alias without colliding with shared `preview` project variables.
  - **Domains (`BaseDomain` + multiple domains)**: When `BaseDomain` is set (e.g. `vercel.cloudvoyant.io`), formats `{project}-{dev|stage|prod|previewId}.{baseDomain}`; otherwise falls back to `<vProjName>-<teamSlug>.vercel.app` (`prod`), `<vProjName>-stage-<teamSlug>.vercel.app` (`stage`), `<vProjName>-dev-<teamSlug>.vercel.app` (`dev`), and `<vProjName>-<previewId>-<teamSlug>.vercel.app` (`preview-*`). Additional vanity domains (`ExtraProdDomains`, `ExtraStageDomains`, `ExtraDevDomains`, and per-deployment `ExtraDomains`) bind multiple `ProjectDomain`s and `Alias`es to the same deployment.
- **`vercel.BlobStore` & `vercel.BlobOverlayClient`**: Derives `https://<slug>.public.blob.vercel-storage.com` from `store_<Slug>`, propagates `BLOB_BASE_URL` and `BLOB_READ_WRITE_TOKEN` into `CustomEnvironmentIds` on `stage`, and exports `BLOB_PREFIX` + `BLOB_FALLBACK_PREFIX`. `BlobOverlayClient` reads from `PrimaryPrefix` first and falls back to `FallbackPrefix` on `404`.
- **`vercel.WebApp`**: When `GCPProject` and `GCPWorkloadIdentityPool` are supplied, creates a `6–30` char GCP Service Account bound with `roles/iam.workloadIdentityUser` to `principalSet://iam.googleapis.com/<pool>/attribute.project_id/<vercelProjectId>`, grants IAM on linked GCP resources, and injects `GCP_WORKLOAD_IDENTITY_PROVIDER` and `GCP_SERVICE_ACCOUNT_EMAIL` into the deployment.

### Interfaces and Contracts

```go
// Package vercel (github.com/cloudvoyant/openbase/libs/components/vercel)
sharedVercel, err := vercel.Shared{
	Name:            "shared-vercel",
	Project:         "storefront",
	TeamID:          "team_...",
	TeamSlug:        "cloudvoyant",
	EnableBlobStore: true,
}.Register(ctx)

blobs, err := vercel.BlobStore{
	Name:           "blobs",
	Project:        "storefront",
	BranchFrom:     "dev",
	SharedStackRef: "org/storefront/shared",
}.Register(ctx)

web, err := vercel.WebApp{
	Name:                    "web",
	Project:                 "storefront",
	Links:                   []core.Resource{api, blobs, db, oidc},
	SharedStackRef:          "org/storefront/shared",
	SourceDir:               "apps/web",
	GCPProject:              pulumi.String("my-gcp-project"),
	GCPWorkloadIdentityPool: gcpPoolName,
	GCPVercelOIDCProvider:   gcpVercelProviderName,
}.Register(ctx)
```

### Synthesized Vercel Resources

| OpenBase Spec | Environment | Synthesized `pulumiverse/pulumi-vercel` v5 Resources |
| :--- | :--- | :--- |
| `vercel.Shared` | `shared` | `Project`, `CustomEnvironment`, `ProjectDomain`, `ProjectProtectionBypass`, optional `BlobStore` + `BlobProjectConnection` |
| `vercel.BlobStore` | `dev`, `stage`, `prod`, `preview-*` | `BlobStore` + `BlobProjectConnection` (or shared store ref), `stage` `ProjectEnvironmentVariable` (`BLOB_BASE_URL`, `BLOB_READ_WRITE_TOKEN`) |
| `vercel.WebApp` | `dev`, `stage`, `prod`, `preview-*` | `ProjectEnvironmentVariable`, `Deployment`, `Alias`, optional GCP `serviceaccount.Account` + `serviceaccount.IAMMember` |

## References

- [OpenBase Architecture Overview](../index.md)
- [OpenBase Resources](../resources.md)
- [GCP Provider](./gcp.md)
- [Neon Provider](./neon.md)
- [Kinde Provider](./kinde.md)
