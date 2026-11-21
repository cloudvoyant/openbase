# OpenBase Go Pulumi components (`libs/components`)

Declarative Go Pulumi components that provision organization platforms, shared application projects, and per-environment compute, object storage, serverless PostgreSQL, and OIDC authentication across GCP, Vercel, Neon, and Kinde.

## Install

```sh
go get github.com/cloudvoyant/openbase/libs/components@v0.1.0
```

## Packages

| Package | Role | Constructors and Helpers |
| --- | --- | --- |
| `libs/components` | Cross-stack `LookupPlatform` and `LookupShared` helpers and supported environment conventions | `LookupPlatform`, `LookupShared`, `SupportedEnvironments` |
| `libs/components/core` | `AccountResource` and `Resource` (`StatelessResource`, `StatefulResource`) contracts, environment rules, domain formulas, naming, tags, `Links`, `ScopeRef`, and `IdentityBroker` | `AccountResource`, `StatelessResource`, `StatefulResource`, `NewCustomAccount`, `NewStateless`, `NewStateful`, `ResolveLinks` |
| `libs/components/gcp` | GCP account foundations, per-app Artifact Registry project, wildcard HTTPS load balancing, Cloud Run v2, `FederatedIdentity` and `WIFBroker`, and GCS storage | `NewAccount`, `LookupAccount`, `NewProject`, `LookupProject`, `NewCloudLoadBalancer`, `NewCloudRunService`, `NewBucket`, `NewFederatedIdentity`, `WIFBroker`, `overlayfs` |
| `libs/components/vercel` | Vercel team account, per-app project (`staging` CustomEnvironment, domains, bypass), WebApp, and Blob storage | `NewAccount`, `LookupAccount`, `NewProject`, `LookupProject`, `NewWebApp`, `NewBlobStore` |
| `libs/components/neon` | Neon org account defaults, per-app Neon Project cluster, and branched serverless PostgreSQL | `NewAccount`, `LookupAccount`, `NewProject`, `LookupProject`, `NewDatabase` |
| `libs/components/kinde` | Kinde Business account (`NonProd` and `Prod` `EnvironmentCredentials`), per-app OIDC `Application` pairs, and environment routing | `NewAccount`, `LookupAccount`, `NewProject`, `LookupProject`, `NewOidcApp` |
| `libs/components/synthetic` | Deterministic offline doubles for fast unit tests | `NewService`, `NewStorage` |

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
