# GCP account and project - software architecture

## Overview

`libs/components/gcp` separates account-level Google Cloud foundations (`gcp.Account`, `core.AccountResource`) from shared repository assets (`gcp.Project`, `core.StatelessResource` with `Environment: "shared"`) over `github.com/pulumi/pulumi-gcp/sdk/v9`. `gcp.Account` runs once per GCP account in `infra/platform` and owns APIs, Workload Identity Federation, KMS, and wildcard load balancing. `gcp.Project` runs once per repository in `infra/shared` and owns strictly the application Docker Artifact Registry (`<project>-gcp`).

## Requirement

### Functional

- Enable required Google Cloud APIs once per GCP account without disabling dependent services on stack teardown.
- Federate GitHub Actions and Vercel OIDC identities at the account level and broker external callers into federated GCP service accounts via `gcp.WIFBroker` and `gcp.FederatedIdentity`.
- Provision optional account KMS key rings and wildcard HTTPS load balancing for Cloud Run applications.
- Provision a dedicated Docker container registry per application repository without threading OIDC pools through project resources.

### Non-Functional

- Reuse one Workload Identity Pool per GCP account across repositories instead of creating duplicate pools per application.
- Support multiple GCP accounts (`"gcp-nonprod"`, `"gcp-prod"`, `"gcp-analytics"`) through typed `gcp.LookupAccount(plat, name...)` handles and explicit `pulumi.Provider(p)` options.

## Design

### Components and Responsibilities

```mermaid
flowchart TD
    subgraph Org["Organization Stack (gcp.NewAccount)"]
        APIs["projects.Service (8 APIs)"]
        WIF["iam.WorkloadIdentityPool"]
        OIDC["Linked OIDC Providers (core.OIDCProviderSource)"]
        KMS["kms.KeyRing"]
        LB["gcp.CloudLoadBalancer (*.gcp.<org>.io)"]
        WIF --> OIDC
    end

    subgraph App["App infra/shared Stack (gcp.NewProject)"]
        AR["artifactregistry.Repository (DOCKER)"]
    end

    subgraph Env["App infra/env Stacks"]
        Broker["gcp.FederatedIdentity / WIFBroker (IdentityBroker)"]
        CR["gcp.CloudRunService"]
        Bkt["gcp.Bucket"]
    end

    Org -->|"gcp.LookupAccount(plat)"| App
    Org -->|"gcp.LookupAccount(plat)"| Broker
    AR -->|"gcp.LookupProject(shared)"| CR
    Broker -->|"Principals[gcp]"| CR
    Broker -->|"Principals[gcp]"| Bkt
    LB -->|"<service>.gcp.<org>.io"| CR
```

- `gcp.Account` enables `run`, `artifactregistry`, `iam`, `iamcredentials`, `sts`, `storage`, `secretmanager`, and `cloudkms` APIs, creates the OIDC pool and providers, and exports `workloadIdentityPoolName`, `<id>OidcProviderName`, `keyRingId`, and `gcpBaseDomain`.
- `gcp.Project` creates `<project>-<name>` in Artifact Registry (omitting `shared` from the repository ID) and exports `repositoryId`, `repositoryUrl`, and `baseDomain`.
- `gcp.FederatedIdentity` and `gcp.WIFBroker` implement `core.IdentityBroker`, minting a federated GCP service account and `roles/iam.workloadIdentityUser` binding when non-GCP workloads (such as `vercel.WebApp`) link GCP resources.
- [gcp.CloudRunService](./cloud-run-service.md) and [gcp.Bucket](./bucket.md) consume `Account: gcp.LookupAccount(plat)` and `Project: gcp.LookupProject(shared)` on `dev`, `stage`, `prod`, and `preview-<slug>`.

### Data and Control Flow

```mermaid
sequenceDiagram
    participant Acc as "gcp.Account (infra/platform)"
    participant Proj as "gcp.Project (infra/shared)"
    participant Svc as "gcp.CloudRunService (infra/env)"
    participant GCP as "Google Cloud APIs"

    Acc->>GCP: 1. Enable APIs + create WIF pool + optional CloudLoadBalancer
    Acc-->>Proj: 2. Export gcpProject + gcpBaseDomain via LookupAccount
    Proj->>GCP: 3. Create Docker Artifact Registry repository
    Proj-->>Svc: 4. Export repositoryId + repositoryUrl via LookupProject
    Svc->>GCP: 5. Bind artifactregistry.reader + deploy Cloud Run v2
```

`gcp.Account` establishes account identity and edge routing once per GCP account. `gcp.Project` creates the application image registry without forwarding WIF pools. Environment stacks read `gcp.LookupProject(shared)` for image pull access and `gcp.LookupAccount(plat)` when crossing cloud boundaries.

### Interfaces and Contracts

```go
// Tier 1: Organization (infra/platform/main.go)
acc, err := gcp.NewAccount(ctx, "gcp", &gcp.AccountArgs{
	Org:                "cloudvoyant",
	GCPProject:         pulumi.String("cloudvoyant-openbase"),
	GCPProjectNumber:   "785485436615",
	Links:              []core.OIDCProviderSource{vercelAcc, core.GitHubActionsOIDC("cloudvoyant")},
	BaseDomain:         "gcp.cloudvoyant.io",
	EnableLoadBalancer: true,
})

// Shared Project Resource in infra/shared/main.go
plat, err := openbase.LookupPlatform(ctx)
proj, err := gcp.NewProject(ctx, "gcp", &gcp.ProjectArgs{
	Account: gcp.LookupAccount(plat),
})
```

| Type | Contract | Synthesized `pulumi-gcp` Resources | Key Outputs |
| :--- | :--- | :--- | :--- |
| `gcp.Account` | `core.AccountResource` | `projects.Service`, `iam.WorkloadIdentityPool`, `iam.WorkloadIdentityPoolProvider`, `kms.KeyRing`, optional `CloudLoadBalancer` | `workloadIdentityPoolName`, `vercelOidcProviderName`, `keyRingId`, `gcpBaseDomain` |
| `gcp.Project` | `core.StatelessResource` (`shared`) | `artifactregistry.Repository` | `repositoryId`, `repositoryUrl`, `baseDomain` |

## Implementation

### Account WIF trust boundary

`gcp.Account` attaches an `iam.WorkloadIdentityPoolProvider` for each generic `core.OIDCProviderSource` in `Links` (such as `vercel.Account` or `core.GitHubActionsOIDC`) without hardcoding external vendor fields in `gcp`. `gcp.WIFBroker` narrows each federated service account further by the caller's `PrincipalClaim` (for example `attribute.project_id/<vercelProjectId>`).

### Cloud Run callback helper

`gcp.BuildCloudRunCallbackURLs` computes deterministic Cloud Run wildcard (`dev` and `preview-*`) and regional or custom-domain (`stage` and `prod`) OAuth callback sets so `kinde.NewProject` can register Cloud Run redirect allowlists in `infra/shared`.

## References

- [Cloud Load Balancer](./cloud-load-balancer.md)
- [Cloud Run Service](./cloud-run-service.md)
- [GCS Bucket and OverlayFS](./bucket.md)
- [OpenBase Architecture Overview](../../index.md)
