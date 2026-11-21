# OpenBase - software architecture

## Overview

OpenBase (`github.com/cloudvoyant/openbase/libs/components`) is a Go Pulumi component library across Google Cloud Platform, Vercel, Neon, and Kinde. It separates infrastructure into three scopes: organization `Platform` foundations in `infra/platform`, repository `Project` assets in `infra/shared`, and per-stack `Environment` deployments in `infra/env`. It does not generate multi-language SDKs, run SQL migrations inside Pulumi state, or target AWS, Azure, or Kubernetes.

## Requirement

### Functional

- Provision organization cloud foundations once per organization and share their outputs across repositories.
- Provision shared project assets once per repository (`shared`) and deploy isolated application stacks across `dev`, `stage`, `prod`, and `preview-<slug>`.
- Branch `stage` stateful data from `prod` and `preview-<slug>` stateful data from `dev`.
- Grant least-privilege IAM and OIDC permissions between linked resources (`Links`) while letting applications wire explicit `Env` and `Secrets` maps from typed resource outputs.

### Non-Functional

- Reject invalid environment names and invalid branch sources during `pulumi preview` before cloud state changes.
- Protect `prod` and `stage` stateful data against accidental deletion while tearing down `preview-<slug>` stacks cleanly on branch close.
- Authenticate across clouds through short-lived OIDC federation (`core.IdentityBroker`) without static service-account keys or cross-provider SDK imports.
- Run offline unit tests in under five seconds without cloud credentials or network access.

## Design

### Components and Responsibilities

```mermaid
flowchart TD
    subgraph Tier1["Tier 1: Organization Platform (infra/platform once per org)"]
        OrgScope["openbase.Platform"] --> GCPPlat["gcp.Platform + CloudLoadBalancer"]
        OrgScope --> VerPlat["vercel.Platform"]
        OrgScope --> NeonPlat["neon.Platform"]
        OrgScope --> KindePlat["kinde.Platform"]
    end

    subgraph Tier2["Tier 2: Application Project (infra/shared per repo)"]
        ProjScope["openbase.Project + openbase.Add"] --> GCPProj["gcp.Project"]
        ProjScope --> VerProj["vercel.Project"]
        ProjScope --> NeonProj["neon.Project"]
        ProjScope --> KindeProj["kinde.Project"]
    end

    subgraph Tier3["Tier 3: Environment Stack (infra/env per dev, stage, prod, preview-*)"]
        EnvScope["openbase.Environment + openbase.Add"] --> CR["gcp.CloudRunService"]
        EnvScope --> Bkt["gcp.Bucket"]
        EnvScope --> Web["vercel.WebApp"]
        EnvScope --> Blob["vercel.BlobStore"]
        EnvScope --> DB["neon.Database"]
        EnvScope --> OIDC["kinde.OidcApp"]
    end

    Tier1 -->|"LookupPlatform / openbaseManifest"| Tier2
    Tier2 -->|"LookupProject / openbaseManifest"| Tier3
```

- `libs/components` (`package components`) implements `Platform`, `Project`, `Environment`, `Add`, `LookupPlatform`, and `LookupProject`.
- `libs/components/core` owns tier contracts, naming bounds, `openbase.io/*` tags, domain formatting, `ScopeRef` manifests, `IdentityBroker`, and `Links`.
- `libs/components/gcp`, `vercel`, `neon`, and `kinde` implement `Platform`, `Project`, and environment product components over upstream or bridged Pulumi providers.
- `libs/components/synthetic` supplies deterministic offline doubles for unit tests.

### Data and Control Flow

```mermaid
sequenceDiagram
    participant Org as "Org infra/platform (openbase.Platform)"
    participant Shared as "App infra/shared (openbase.Project)"
    participant Env as "App infra/env (openbase.Environment on preview-pr-42)"
    participant State as "Shared Pulumi State Backend"
    participant Cloud as "GCP / Vercel / Neon / Kinde"

    Org->>Cloud: 1. Create WIF pool, wildcard LB, team defaults, Kinde tenants
    Org-->>State: 2. Export org outputs + openbaseManifest
    Shared->>State: 3. Read LookupPlatform / PlatformStackRef
    Shared->>Cloud: 4. Create Artifact Registry, Vercel Project, Neon Project, Kinde Apps
    Shared-->>State: 5. Export project outputs + openbaseManifest (<project>-shared/shared)
    Env->>State: 6. Read LookupProject + dev parent stack
    Env->>Cloud: 7. Create CoW Neon branch, storage overlay, and compute deployment
```

An organization deploys `openbase.Platform` once into its shared Pulumi backend bucket and exports identity and domain roots. Each repository deploys `openbase.Project` once in `infra/shared` and exports application identifiers and `openbaseManifest`. Each `dev`, `stage`, `prod`, or `preview-<slug>` stack deploys `openbase.Environment`, branches stateful data automatically, grants IAM access through `Links`, and binds explicit `Env` and `Secrets` from typed resource outputs.

### Interfaces and Contracts

| Scope | Go Interface | Scope Fields | Canonical Name | Allowed Stacks |
| :--- | :--- | :--- | :--- | :--- |
| Organization Platform | `core.PlatformResource` | `Name`, `Org` | `<org>-<name>` | `platform` (`infra/platform`) |
| Shared Resource | `core.StatelessResource` / `core.StatefulResource` | `Name`, `Project`, `Repo`, `Environment: "shared"` | `<project>-<name>` | `shared` (`infra/shared`) |
| Environment Resource | `core.StatelessResource` / `core.StatefulResource` | `Name`, `Project`, `Repo`, `Environment`, `BranchFrom`, `Links` | `<project>-<env>-<name>` | `dev`, `stage`, `prod`, `preview-<slug>` (`infra/env`) |

## Implementation

### Provider bindings

`gcp` and `vercel` import `github.com/pulumi/pulumi-gcp/sdk/v9` and `github.com/pulumiverse/pulumi-vercel/sdk/v5` independently, with zero `pulumi-gcp` imports inside `vercel`. `neon` (`neondatabase/neon`) and `kinde` (`kinde-oss/kinde`) import internal generated Go SDK packages in `libs/internal/pulumi-neon-go-sdk` and `libs/internal/pulumi-kinde-go-sdk`. Run `mise run generate:check` in CI to make sure that generated bindings have zero drift.

### Shared vs environment naming

Every non-platform component satisfies `core.Resource` (`StatelessResource` or `StatefulResource`). When `Environment` is `dev`, `stage`, `prod`, or `preview-<slug>`, `core.CanonicalName` formats `<project>-<env>-<name>`. When `Environment` is `shared`, `core.CanonicalName` omits `env` and formats `<project>-<name>` so shared Artifact Registry repositories, Neon projects, and project load balancers stay concise.

## References

- [User Guide](../user-guide.md)
- [Unified Scopes and Manifests](./scopes.md)
- [Core Resources and Tier Contracts](./resources.md)
- [GCP Provider](./providers/gcp/index.md)
- [Vercel Provider](./providers/vercel/index.md)
- [Neon Provider](./providers/neon/index.md)
- [Kinde Provider](./providers/kinde/index.md)
