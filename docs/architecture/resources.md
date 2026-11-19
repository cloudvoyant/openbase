# Core resources and platform contracts - software architecture

## Overview

`libs/components/core` defines two top-level contracts: `PlatformResource` for organization foundations and `Resource` (`StatelessResource` and `StatefulResource`) for all shared and per-environment resources. It owns naming rules, domain formulas, `openbase.io/*` tags, and typed `Links`. It does not import any cloud provider SDK.

## Requirement

### Functional

- FR1: Provide `InitPlatform` (`PlatformMeta`) for organization foundations and `InitStateless` / `InitStateful` (`StatelessMeta` / `StatefulMeta`) for resources across `shared`, `dev`, `stage`, `prod`, and `preview-<slug>`.
- FR2: Format `<project>-<env>-<name>` canonical names on environment stacks and `<project>-<name>` canonical names (omitting `env`) when a resource declares `shared`.
- FR3: Resolve `Links` into merged environment maps, secret maps, and least-privilege `GrantAccess` callbacks while rejecting duplicate variable keys.

### Non-Functional

- NFR1: Fail fast during `pulumi preview` on malformed identifiers, unsupported environments, self-referential `BranchFrom` targets, or colliding link keys.
- NFR2: Keep every handwritten function at or below cyclomatic complexity `10`.

## Design

### Components and Responsibilities

```mermaid
classDiagram
    class PlatformResource {
        <<interface>>
        +OpenBasePlatform() PlatformInfo
        +Link() LinkDescriptor
    }
    class Resource {
        <<interface>>
        +OpenBaseMeta() ResourceMeta
        +Link() LinkDescriptor
    }
    class StatelessResource {
        <<interface>>
    }
    class StatefulResource {
        <<interface>>
        +BranchSource() string
    }
    Resource <|-- StatelessResource
    Resource <|-- StatefulResource
```

- `tier.go` defines `PlatformResource`, `PlatformInfo`, `PlatformMeta`, `InitPlatform`, and `GCPPlatformLabels`.
- `environment.go` parses `shared`, `dev`, `stage`, `prod`, and `preview-<slug>` stacks and validates `BranchFrom` parents.
- `resource.go` defines `Resource`, `StatelessResource`, `StatefulResource`, and custom escape-hatch specs (`core.Stateless` and `core.Stateful`).
- `links.go` merges `Env` and `Secrets` outputs across linked resources and invokes `GrantAccess` callbacks.
- `naming.go` and `domain.go` omit `env` when `Environment` is `shared` and enforce cloud length bounds with deterministic SHA-256 truncation suffixes.

### Data and Control Flow

```mermaid
flowchart LR
    Producer["Stateful / Stateless Producer"] -->|"SetLink(Env, Secrets, GrantAccess)"| Link["LinkDescriptor"]
    Link -->|"Links: []core.Resource"| Resolver["core.ResolveLinks"]
    Resolver -->|"Make sure keys are unique"| Merged["ResolvedLinks (Env + Secrets)"]
    Resolver -->|"GrantAccess(ConsumerIdentity)"| IAM["Scoped IAM / OIDC Binding"]
    Merged --> Workload["CloudRunService / WebApp"]
```

A producer registers its `LinkDescriptor` with plain outputs, secret outputs, and an optional `GrantAccess` callback. When a consumer passes `Links`, `ResolveLinks` merges all keys, rejects collisions with `ErrDuplicateLinkEnvKey`, and triggers `GrantAccess` with the consumer identity.

### Interfaces and Contracts

```go
type PlatformResource interface {
	pulumi.Resource
	OpenBasePlatform() PlatformInfo
	Link() LinkDescriptor
}

type Resource interface {
	pulumi.Resource
	OpenBaseMeta() ResourceMeta
	Link() LinkDescriptor
}

type StatelessResource interface{ Resource }

type StatefulResource interface {
	Resource
	BranchSource() string
}
```

| Environment | Canonical Name | `IsPersistent()` | `IsProtected()` | `IsEphemeral()` | Valid `BranchFrom` |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `shared` | `<project>-<name>` | `false` | `false` | `false` | `""` |
| `dev` | `<project>-dev-<name>` | `true` | `false` | `false` | `""`, `"stage"`, `"prod"` |
| `stage` | `<project>-stage-<name>` | `true` | `true` | `false` | `""`, `"dev"`, `"prod"` |
| `prod` | `<project>-prod-<name>` | `true` | `true` | `false` | `""`, `"dev"`, `"stage"` |
| `preview-<slug>` | `<project>-preview-<slug>-<name>` | `false` | `false` | `true` | `""`, `"dev"`, `"stage"`, `"prod"` |

## Implementation

### Shared naming rule

When `env == EnvShared`, `CanonicalName` returns `<project>-<name>` instead of `<project>-shared-<name>`, and `HostLabel` returns `<project>` (for `web`) or `<project>-<service>` (for named services). Any concrete resource such as `gcp.CloudLoadBalancer` or `gcp.Bucket` can therefore deploy as a `shared` project resource or as an environment resource without redundant `-shared-` tokens in cloud names.

### Escape hatches

Teams can wrap any raw Pulumi resource in `core.Stateless` or `core.Stateful` with a `RegisterFunc` callback. The wrapper applies the same environment rules, `Protect` flags, `openbase.io/*` tags, and `Links` contract as built-in provider components.

## References

- [OpenBase Architecture Overview](./index.md)
- [GCP Provider](./providers/gcp/index.md)
- [Vercel Provider](./providers/vercel/index.md)
- [Neon Provider](./providers/neon/index.md)
- [Kinde Provider](./providers/kinde/index.md)
- [Composite Blueprints](./providers/composite/index.md)
