# OpenBase - software architecture

## Overview

OpenBase (`github.com/cloudvoyant/openbase/libs/components`) is a Go Pulumi component library across Google Cloud Platform, Vercel, Neon, and Kinde. It separates infrastructure into three strict tiers: organization `Platform` foundations, repository `Project` assets in `infra/shared`, and per-stack `Environment` deployments in `infra/`. It does not generate multi-language SDKs, run SQL migrations inside Pulumi state, or target AWS, Azure, or Kubernetes.

## Requirement

### Functional

- Provision organization cloud foundations once per organization and share their outputs across repositories.
- Provision shared project assets once per repository (`shared`) and deploy isolated application stacks across `dev`, `stage`, `prod`, and `preview-<slug>`.
- Branch `stage` stateful data from `prod` and `preview-<slug>` stateful data from `dev`.
- Bind environment variables, secrets, and least-privilege IAM or OIDC permissions between linked resources.

### Non-Functional

- Reject invalid environment names, invalid branch sources, and colliding link keys during `pulumi preview` before cloud state changes.
- Protect `prod` and `stage` stateful data against accidental deletion while tearing down `preview-<slug>` stacks cleanly on branch close.
- Authenticate across clouds through short-lived OIDC federation without static service-account keys.
- Run offline unit tests in under two seconds without cloud credentials or network access.

## Design

### Components and Responsibilities

```mermaid
flowchart TD
    subgraph Tier1["Tier 1: Organization Platform (once per org)"]
        CompPlat["composite.Platform"] --> GCPPlat["gcp.Platform + CloudLoadBalancer"]
        CompPlat --> VerPlat["vercel.Platform"]
        CompPlat --> NeonPlat["neon.Platform"]
        CompPlat --> KindePlat["kinde.Platform"]
    end

    subgraph Tier2["Tier 2: Application Project (infra/shared per repo)"]
        CompProj["VercelNeonProject / GcpCloudRunNeonProject"] --> GCPProj["gcp.Project"]
        CompProj --> VerProj["vercel.Project"]
        CompProj --> NeonProj["neon.Project"]
        CompProj --> KindeProj["kinde.Project"]
    end

    subgraph Tier3["Tier 3: Environment Stack (infra/ per dev, stage, prod, preview-*)"]
        CompApp["VercelNeonApp / GcpCloudRunNeonApp"] --> CR["gcp.CloudRunService"]
        CompApp --> Bkt["gcp.Bucket"]
        CompApp --> Web["vercel.WebApp"]
        CompApp --> Blob["vercel.BlobStore"]
        CompApp --> DB["neon.Database"]
        CompApp --> OIDC["kinde.OidcApp"]
    end

    Tier1 -->|"PlatformStackRef"| Tier2
    Tier2 -->|"ProjectStackRef"| Tier3
```

- `libs/components/core` owns tier contracts, naming bounds, `openbase.io/*` tags, domain formatting, and `Links`.
- `libs/components/gcp`, `vercel`, `neon`, and `kinde` implement `Platform`, `Project`, and environment product components over upstream or bridged Pulumi providers.
- `libs/components/composite` wires multi-provider blueprints for Premise repositories (`infra/shared` and `infra/`).
- `libs/components/synthetic` supplies deterministic offline doubles for unit tests.

### Data and Control Flow

```mermaid
sequenceDiagram
    participant Org as "Org Repo (composite.Platform)"
    participant Shared as "App infra/shared (*Project)"
    participant Env as "App infra/ (*App on preview-pr-42)"
    participant State as "Pulumi State Backend"
    participant Cloud as "GCP / Vercel / Neon / Kinde"

    Org->>Cloud: 1. Create WIF pool, wildcard LB, team defaults, Kinde tenants
    Org-->>State: 2. Export org outputs
    Shared->>State: 3. Read PlatformStackRef
    Shared->>Cloud: 4. Create Artifact Registry, Vercel Project, Neon Project, Kinde Apps
    Shared-->>State: 5. Export project outputs (<project>-shared/shared)
    Env->>State: 6. Read ProjectStackRef + dev parent stack
    Env->>Cloud: 7. Create CoW Neon branch, storage overlay, and compute deployment
```

An organization deploys `Platform` once and exports shared identity and domain roots. Each repository deploys `Project` once in `infra/shared` and exports application identifiers. Each `dev`, `stage`, `prod`, or `preview-<slug>` stack reads `ProjectStackRef`, branches stateful data when configured, and binds runtime secrets and IAM grants through `Links`.

### Interfaces and Contracts

| Scope | Go Interface | Scope Fields | Canonical Name | Allowed Stacks |
| :--- | :--- | :--- | :--- | :--- |
| Organization Platform | `core.PlatformResource` | `Name`, `Org` | `<org>-<name>` | Central org stack |
| Shared Resource | `core.StatelessResource` / `core.StatefulResource` | `Name`, `Project`, `Repo`, `Environment: "shared"` | `<project>-<name>` | `shared` (`infra/shared`) |
| Environment Resource | `core.StatelessResource` / `core.StatefulResource` | `Name`, `Project`, `Repo`, `Environment`, `BranchFrom`, `Links` | `<project>-<env>-<name>` | `dev`, `stage`, `prod`, `preview-<slug>` |

## Implementation

### Provider bindings

`gcp` and `vercel` import `github.com/pulumi/pulumi-gcp/sdk/v9` and `github.com/pulumiverse/pulumi-vercel/sdk/v5`. `neon` (`kislerdm/neon`) and `kinde` (`kinde-oss/kinde`) use generated Go packages in `internal/providers/{neon,kinde}`. Run `mise run generate:check` in CI to make sure that generated bindings have zero drift.

### Shared vs environment naming

Every non-platform component satisfies `core.Resource` (`StatelessResource` or `StatefulResource`). When `Environment` is `dev`, `stage`, `prod`, or `preview-<slug>`, `core.CanonicalName` formats `<project>-<env>-<name>`. When `Environment` is `shared`, `core.CanonicalName` omits `env` and formats `<project>-<name>` so shared Artifact Registry repositories, Neon projects, and project load balancers stay concise.

## References

- [User Guide](../user-guide.md)
- [Core Resources and Tier Contracts](./resources.md)
- [GCP Provider](./providers/gcp/index.md)
- [Vercel Provider](./providers/vercel/index.md)
- [Neon Provider](./providers/neon/index.md)
- [Kinde Provider](./providers/kinde/index.md)
- [Composite Blueprints](./providers/composite/index.md)
