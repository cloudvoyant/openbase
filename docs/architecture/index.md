# OpenBase - software architecture

## Overview

OpenBase (`github.com/cloudvoyant/openbase/libs/components`) is a Go Pulumi component library across Google Cloud Platform, Vercel, Neon, and Kinde. It separates infrastructure into three strict tiers: organization `Platform` foundations, repository `Project` assets in `infra/shared`, and per-stack `Environment` deployments in `infra/`. It does not generate multi-language SDKs, run SQL migrations inside Pulumi state, or target AWS, Azure, or Kubernetes.

## Requirement

### Functional

- FR1: Enforce three distinct Go interfaces (`core.PlatformResource`, `core.ProjectResource`, and `core.EnvironmentResource`) so organization, project, and environment resources never mix concerns.
- FR2: Provision stateless compute (`gcp.CloudRunService`, `vercel.WebApp`), OIDC routing (`kinde.OidcApp`), and stateful data (`gcp.Bucket`, `vercel.BlobStore`, `neon.Database`) across `dev`, `stage`, `prod`, and `preview-<slug>` stacks.
- FR3: Protect `prod` and `stage` stateful resources against deletion, branch `stage` off `prod` and `preview-<slug>` off `dev`, and bind environment variables, secrets, and IAM grants through `Links`.

### Non-Functional

- NFR1: Reject invalid environment names, invalid `BranchFrom` parents, and duplicate link keys during `pulumi preview` before cloud state changes.
- NFR2: Run offline `pulumi.WithMocks` unit suites in under two seconds without cloud credentials.
- NFR3: Keep a single root `go.mod` (`GOWORK=off`, no nested `go.mod`) so Premise publishes Go releases from repository Git tags.

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

| Tier | Go Interface | Scope Fields | Canonical Name | Allowed Stacks |
| :--- | :--- | :--- | :--- | :--- |
| Organization | `core.PlatformResource` | `Name`, `Org` | `<org>-platform-<name>` | Central org stack |
| Project | `core.ProjectResource` | `Name`, `Project`, `Repo` | `<project>-project-<name>` | `shared` (`infra/shared`) |
| Environment (Stateless) | `core.StatelessResource` | `Name`, `Project`, `Repo`, `Environment`, `Links` | `<project>-<env>-<name>` | `dev`, `stage`, `prod`, `preview-<slug>` |
| Environment (Stateful) | `core.StatefulResource` | `Name`, `Project`, `Repo`, `Environment`, `BranchFrom`, `Links` | `<project>-<env>-<name>` | `dev`, `stage`, `prod`, `preview-<slug>` |

## Implementation

### Provider bindings

`gcp` and `vercel` import `github.com/pulumi/pulumi-gcp/sdk/v9` and `github.com/pulumiverse/pulumi-vercel/sdk/v5`. `neon` (`kislerdm/neon`) and `kinde` (`kinde-oss/kinde`) use generated Go packages in `internal/providers/{neon,kinde}`. Run `mise run generate:check` in CI to make sure that generated bindings have zero drift.

### Stack boundaries

`core.ParseEnvironment` accepts only `dev`, `stage`, `prod`, and `preview-<slug>`. It rejects `shared`, `project`, and `platform` so environment components never deploy onto a project stack. `core.RequireProjectStack` rejects `dev`, `stage`, `prod`, and `preview-<slug>` so Premise `infra/shared` blueprints never deploy onto an environment stack.

## References

- [User Guide](../user-guide.md)
- [Core Resources and Tier Contracts](./resources.md)
- [GCP Provider](./providers/gcp/index.md)
- [Vercel Provider](./providers/vercel/index.md)
- [Neon Provider](./providers/neon/index.md)
- [Kinde Provider](./providers/kinde/index.md)
- [Composite Blueprints](./providers/composite/index.md)
