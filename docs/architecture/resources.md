# Core resources and platform contracts - software architecture

## Overview

`libs/components/core` defines two top-level contracts: `PlatformResource` for organization foundations and `Resource` (`StatelessResource` and `StatefulResource`) for all shared and per-environment resources. It owns naming rules, domain formulas, `openbase.io/*` tags, `ScopeRef` manifest lookups, provider-neutral `IdentityBroker` federation, and typed `Links`. It imports zero cloud provider SDKs.

## Requirement

### Functional

- Validate resource identifiers, deployment environments (`shared`, `dev`, `stage`, `prod`, `preview-<slug>`), and stateful branch parents.
- Generate bounded, deterministic cloud resource names and hostnames that omit the environment token when a resource is `shared`.
- Resolve external workload OIDC `Subject` identities into cloud IAM `Principals` once per consumer through `IdentityBroker` and pass them into linked `GrantAccess` hooks.
- Allow callers to wrap custom Pulumi resources with the same lifecycle, tagging, scope registration, and linking contract as built-in components.

### Non-Functional

- Fail during `pulumi preview` before registering cloud resources when inputs violate naming or branching rules.
- Keep secret resource outputs encrypted in Pulumi state and output maps, and read zero ambient process environment variables inside `libs/components`.
- Stay free of cloud provider SDK dependencies so `core` tests run offline in milliseconds.

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
    class IdentityBroker {
        <<interface>>
        +BrokerProvider() string
        +ResolvePrincipal(ctx, meta, sub) PrincipalGrant
    }
    Resource <|-- StatelessResource
    Resource <|-- StatefulResource
```

- `platform.go` defines `PlatformResource`, `PlatformInfo`, `PlatformMeta`, `InitPlatform`, `GCPPlatformLabels`, and the `core.Platform` escape hatch.
- `environment.go` parses `shared`, `dev`, `stage`, `prod`, and `preview-<slug>` stacks and validates `BranchFrom` parents.
- `resource.go` defines `Resource`, `StatelessResource`, `StatefulResource`, and custom escape-hatch specs (`core.Stateless` and `core.Stateful`).
- `links.go` defines `Subject`, `PrincipalGrant`, `IdentityBroker`, `LinkGrantContext`, and `ResolveLinks`.
- `scope.go` defines `Scope`, `PlatformSpecItem`, `ScopedSpec`, `ScopedSpecOf[R]`, `ScopeRef`, `RemoteResource`, and `BuildManifestMap`.
- `naming.go` and `domain.go` omit `env` when `Environment` is `shared` and enforce cloud length bounds with deterministic SHA-256 truncation suffixes.

### Data and Control Flow

```mermaid
flowchart LR
    Consumer["Consumer Subject (gcp / vercel)"] --> Resolver["core.ResolveLinks"]
    Brokers["[]core.IdentityBroker (FederatedIdentity / WIFBroker)"] -->|"ResolvePrincipal once"| Resolver
    Producer["Linked Producer / RemoteResource"] -->|"LinkDescriptor.GrantAccess"| Resolver
    Resolver -->|"Principals[provider]"| IAM["Least-Privilege Cloud IAM Binding"]
```

A consumer passes its provider-neutral `Subject` and `Brokers` into `core.ResolveLinks`. Each `IdentityBroker` translates external OIDC claims into a target-cloud IAM member once (`Principals["gcp"]`). `ResolveLinks` then invokes each linked resource's `GrantAccess` hook without injecting unsolicited environment variable names into the consumer workload.

### Interfaces and Contracts

```go
type Subject struct {
	Provider string
	NativeID pulumi.StringOutput
	Claims   map[string]pulumi.StringOutput
}

type IdentityBroker interface {
	BrokerProvider() string
	ResolvePrincipal(ctx *pulumi.Context, consumer ResourceMeta, sub Subject) (PrincipalGrant, error)
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

When `env == EnvShared`, `CanonicalName` returns `<project>-<name>` instead of `<project>-shared-<name>`, and `HostLabel` returns `<project>` (for `web`) or `<project>-<service>` (for named services). Any concrete resource such as `gcp.CloudLoadBalancer` or `gcp.Bucket` can deploy as a `shared` project resource or as an environment resource without redundant `-shared-` tokens in cloud names.

### Escape hatches

Teams can wrap any raw Pulumi resource in `core.Platform`, `core.Stateless`, or `core.Stateful`. Each escape hatch implements `PlatformSpecItem` or `ScopedSpecOf`, so custom resources plug directly into `openbase.Platform`, `openbase.Project`, `openbase.Environment`, and `openbase.Add`.

## References

- [OpenBase Architecture Overview](./index.md)
- [Unified Scopes and Manifests](./scopes.md)
- [GCP Provider](./providers/gcp/index.md)
- [Vercel Provider](./providers/vercel/index.md)
- [Neon Provider](./providers/neon/index.md)
- [Kinde Provider](./providers/kinde/index.md)
