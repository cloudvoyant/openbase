# OpenBase Go Pulumi components (`libs/components`)

Declarative Go Pulumi components that provision organization platforms, shared application projects, and per-environment compute, object storage, serverless PostgreSQL, and OIDC authentication across GCP, Vercel, Neon, and Kinde.

## Install

```sh
go get github.com/cloudvoyant/openbase/libs/components@v0.1.0
```

## Packages

| Package | Role | Declarative Specs |
| --- | --- | --- |
| `libs/components` | Unified `Platform`, `Project`, and `Environment` scopes, typed `Add`, and cross-stack `LookupPlatform` and `LookupProject` | `Platform`, `Project`, `Environment`, `NewProject`, `NewEnvironment`, `Add`, `LookupPlatform`, `LookupProject` |
| `libs/components/core` | `PlatformResource` and `Resource` (`StatelessResource`, `StatefulResource`) contracts, environment rules, domain formulas, naming, tags, `Links`, and `IdentityBroker` | `PlatformResource`, `StatelessResource`, `StatefulResource`, `Platform`, `Stateless`, `Stateful`, `ResolveLinks` |
| `libs/components/gcp` | GCP org platform, per-app Artifact Registry project, wildcard HTTPS load balancing, Cloud Run v2, `FederatedIdentity` and `WIFBroker`, and GCS storage | `Platform`, `Project`, `CloudLoadBalancer`, `CloudRunService`, `Bucket`, `FederatedIdentity`, `WIFBroker`, `overlayfs` |
| `libs/components/vercel` | Vercel team platform, per-app project (`staging` CustomEnvironment, domains, bypass), WebApp, and Blob storage | `Platform`, `Project`, `WebApp`, `BlobStore` |
| `libs/components/neon` | Neon org defaults, per-app Neon Project cluster, and branched serverless PostgreSQL | `Platform`, `Project`, `Database` |
| `libs/components/kinde` | Two-tenant Kinde org connections (`NonProd` and `Prod`), per-app OIDC `Application` pairs, and environment routing | `Platform`, `Project`, `OidcApp` |
| `libs/components/synthetic` | Deterministic offline doubles for fast unit tests | `Service`, `Storage` |

Standalone consumer fixtures and live cloud end-to-end suites live in [e2e/components](../../e2e/README.md).

## Develop

```sh
mise run install
mise run format:check
mise run lint
mise run test
mise run build
```

Formatting and lint tasks read the root `.golangci.yml` configuration. `libs/components` publishes (`package.public: true`) through the repository `vX.Y.Z` Git tag in Premise.
