# Kinde platform and project - software architecture

## Overview

`libs/components/kinde` separates organization two-tenant Kinde connections (`kinde.Platform`, `core.PlatformResource`) from shared OIDC client registrations (`kinde.Project`, `core.StatelessResource` with `Environment: "shared"`) over the bridged `kinde-oss/kinde` provider (`libs/internal/pulumi-kinde-go-sdk`). `kinde.Platform` runs once per organization to configure `NonProd` and `Prod` Kinde tenants and discover shared authentication connections. `kinde.Project` runs once per repository in `infra/shared` to register paired `NonProd` and `Prod` OIDC applications.

## Requirement

### Functional

- Connect to separate `NonProd` and `Prod` Kinde tenants and discover shared authentication strategies once per organization.
- Register paired `NonProd` and `Prod` OAuth2/OIDC applications per repository.
- Allow localhost, `dev`, and wildcard preview callback URLs on the `NonProd` tenant while restricting the `Prod` tenant to `stage` and `prod` domains.

### Non-Functional

- Keep shared `NonProd` and `Prod` OIDC applications intact when ephemeral pull-request previews are destroyed.
- Avoid per-pull-request mutations to Kinde callback allowlists so concurrent previews never race.
- Keep management M2M credentials and OIDC client secrets encrypted in Pulumi state.

## Design

### Components and Responsibilities

```mermaid
flowchart TD
    subgraph Org["Organization Stack (kinde.Platform)"]
        NonProdProv["kinde.Provider (NonProd tenant)"]
        ProdProv["kinde.Provider (Prod tenant)"]
        ConnLookup["GetConnections (email:password)"]
        NonProdProv --> ConnLookup
    end

    subgraph App["App infra/shared Stack (kinde.Project)"]
        NonProdApp["kinde.Application (NonProd: localhost + dev + preview wildcards)"]
        NonProdConn["kinde.ApplicationConnection (email:password)"]
        ProdApp["kinde.Application (Prod: stage + prod hosts)"]
        NonProdApp --> NonProdConn
    end

    subgraph Env["App infra/env Stacks"]
        EnvOidc["kinde.OidcApp (dev/preview -> NonProd, stage/prod -> Prod)"]
    end

    Org --> App
    NonProdApp --> EnvOidc
    ProdApp --> EnvOidc
```

- `kinde.Platform` configures both tenant providers and caches `emailPasswordConn`.
- `kinde.Project` creates one OIDC application per tenant, binds `email:password` on `NonProd`, and exports `nonProdClientId`, `nonProdClientSecret`, `prodClientId`, and `prodClientSecret`.
- [kinde.OidcApp](./oidc-app.md) binds environment-specific redirect URLs on `dev`, `stage`, `prod`, and `preview-<slug>`.

### Data and Control Flow

```mermaid
sequenceDiagram
    participant Shared as "kinde.Project (infra/shared)"
    participant NonProdT as "Kinde NonProd Tenant"
    participant ProdT as "Kinde Prod Tenant"
    participant Env as "kinde.OidcApp (infra/env)"

    Shared->>NonProdT: 1. Create Application + localhost/dev/preview wildcard callbacks
    Shared->>NonProdT: 2. Attach email:password ApplicationConnection
    Shared->>ProdT: 3. Create Application + stage & prod callbacks
    Shared-->>Env: 4. Export nonProd* and prod* credentials via StackReference
```

`kinde.Project` registers wildcard callback patterns once in `infra/shared`. Because Kinde supports wildcard subdomain callbacks on `NonProd`, pull-request stacks never mutate Kinde application allowlists or race with concurrent PRs.

### Interfaces and Contracts

```go
// Tier 1: Organization (core.PlatformResource)
plat, err := kinde.Platform{
	Org:     "cloudvoyant",
	NonProd: nonProdTenantCreds,
	Prod:    prodTenantCreds,
}.Register(ctx)

// Shared Project Resource in infra/shared (core.StatelessResource)
proj, err := kinde.Project{
	Project:          "storefront",
	Repo:             "cloudvoyant/storefront",
	PlatformStackRef: "cloudvoyant/platform/platform",
	VercelTeamSlug:   "cloudvoyant",
	BaseDomain:       "vercel.cloudvoyant.io",
}.Register(ctx)
```

| Spec | Contract | Synthesized `kinde` Resources | Key Outputs |
| :--- | :--- | :--- | :--- |
| `kinde.Platform` | `core.PlatformResource` | 2x `kinde.Provider`, `GetConnections` | `nonProdIssuerUrl`, `prodIssuerUrl`, `emailPasswordConn` |
| `kinde.Project` | `core.StatelessResource` (`shared`) | 2x `kinde.Application`, `kinde.ApplicationConnection` | `nonProdIssuerUrl`, `nonProdClientId`, `nonProdClientSecret`, `prodIssuerUrl`, `prodClientId`, `prodClientSecret` |

## Implementation

### Sandbox import and retention

`ImportNonProdAppID`, `ImportProdAppID`, and `RetainApplicationsOnDelete` allow E2E suites and existing applications to adopt pre-existing Kinde OIDC applications and update callback lists in place without deleting tenant apps on teardown.

### Issuer normalization

`NormalizeIssuerURL` trims trailing slashes and prepends `https://` when omitted so `KINDE_ISSUER_URL` and `<issuer>/api` management audiences stay consistent.

## References

- [Kinde OidcApp](./oidc-app.md)
- [OpenBase Architecture Overview](../../index.md)
