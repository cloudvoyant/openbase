# Neon Database - software architecture

## Overview

`neon.Database` (`core.StatefulResource`) provisions per-environment serverless PostgreSQL branches, compute endpoints, roles, and databases across `dev`, `stage`, `prod`, and `preview-<slug>` stacks. It uses Neon copy-on-write branching so `stage` branches from `prod` and `preview-<slug>` branches from `dev` in seconds without copying storage pages.

## Requirement

### Functional

- Provision isolated PostgreSQL branches and compute endpoints for `dev`, `stage`, `prod`, and `preview-<slug>` stacks.
- Branch `stage` from `prod` and `preview-<slug>` from `dev` with inherited schemas, roles, and seed data.
- Export pooled connection URLs for serverless workloads and direct connection URLs for schema migrations.

### Non-Functional

- Branch child databases in seconds via copy-on-write storage without duplicating storage pages or colliding on existing roles.
- Protect `prod` and `stage` database branches from accidental deletion while deleting ephemeral preview branches on teardown.
- Scale idle `dev` and `preview-<slug>` compute endpoints to zero to control database cost.

## Design

### Components and Responsibilities

```mermaid
flowchart TD
    Proj["neon.Project (main)"] --> Dev["neon.Database: dev (Role + Database + Endpoint)"]
    Proj --> Prod["neon.Database: prod (Protect=true, Role + Database + Endpoint)"]
    Prod -->|"BranchFrom: prod"| Stage["neon.Database: stage (Protect=true, CoW Branch + Endpoint)"]
    Dev -->|"BranchFrom: dev"| Prev["neon.Database: preview-pr-42 (CoW Branch + Endpoint)"]
    Prev -->|"DatabaseURL (-pooler) + DirectURL"| App["CloudRunService / WebApp"]
```

- `neon.Branch` snapshots either `defaultBranchId` (root tier) or the `BranchFrom` stack `branchId` (child tier).
- `neon.Endpoint` creates an isolated read-write compute endpoint with `-pooler` PgBouncer pooling (`HostPooler`).
- `neon.Role` and `neon.Database` run only on root branches (`BranchFrom == ""`).
- `revealRolePasswordOutput` fetches the role password from the Neon v2 `reveal_password` API using the explicit `APIKey` input (inherited from `neon.Platform` or `neon.Project` when in-memory) because `neondatabase/neon` `0.19.0-beta` omits role passwords from provider state.

### Data and Control Flow

```mermaid
sequenceDiagram
    participant Pulumi as "Pulumi (preview-pr-42)"
    participant DevRef as "StackReference (dev)"
    participant Neon as "Neon API"
    participant App as "Caller Workload"

    Pulumi->>DevRef: 1. Read parent branchId
    Pulumi->>Neon: 2. Create CoW Branch (parentId = dev.branchId)
    Pulumi->>Neon: 3. Create read_write Endpoint
    Pulumi->>Neon: 4. Fetch role password via reveal_password
    Neon-->>Pulumi: 5. Return host, hostPooler, and password
    Pulumi-->>App: 6. Expose secret DatabaseURL + DirectURL outputs
```

When `preview-pr-42` runs `pulumi up`, it branches instantaneously from `dev` with all schema migrations and seed rows intact. SQL writes on `preview-pr-42` stay isolated from `dev`, and `pulumi destroy` removes the preview branch and endpoint cleanly.

### Interfaces and Contracts

```go
db, err := neon.NewDatabase(ctx, "db", &neon.DatabaseArgs{
	Project: "storefront",
	Shared:  shared,
	APIKey:  cfg.RequireSecret("neonApiKey"),
})
```

| Output Field | Secret | Host Target | Purpose |
| :--- | :--- | :--- | :--- |
| `PoolerHost` | No | `Endpoint.HostPooler` | Pooled PgBouncer hostname (`-pooler`) |
| `Host` | No | `Endpoint.Host` | Direct unpooled PostgreSQL hostname |
| `DatabaseURL` | Yes | `Endpoint.HostPooler` | Pooled connection URL for serverless workloads |
| `DirectURL` | Yes | `Endpoint.Host` | Direct connection URL for schema migrations (`pm migrate`) |

## Implementation

### Root vs child branch split

`provisionRootRoleAndDB` creates `neon.Role` (`openbase_app`) and `neon.Database` (`openbase`) when `BranchFrom` is empty. When `BranchFrom` is set, `readInheritedRolePassword` skips role and database creation because Neon copies catalog objects from the parent branch automatically.

### Connection URL escaping

`buildPostgresURLs` percent-encodes passwords via `url.UserPassword` and sets `sslmode=require` on both pooled and direct URLs.

## References

- [Neon Platform and Project](./index.md)
- [Core Resources and Tier Contracts](../../resources.md)
