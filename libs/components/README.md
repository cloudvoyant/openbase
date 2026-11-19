# OpenBase Go Pulumi components (`libs/components`)

Declarative Go Pulumi components for provisioning organization platforms, shared application projects, and per-environment services, object storage, serverless PostgreSQL, and OIDC authentication across GCP, Vercel, Neon, and Kinde.

## Install

```sh
go get github.com/cloudvoyant/openbase/libs/components@v0.1.0
```

## Packages

| Package | Role | Declarative Specs |
| --- | --- | --- |
| `libs/components/core` | Lifecycle contracts, environment validation, domain conventions, naming, tags, and `Links` | `Stateless`, `Stateful`, `StatelessResource`, `StatefulResource`, `ResolveLinks` |
| `libs/components/gcp` | GCP org platform, per-app Artifact Registry project, wildcard HTTPS load balancing, Cloud Run v2, and GCS storage | `Platform`, `Project`, `CloudLoadBalancer`, `CloudRunService`, `Bucket`, `overlayfs` |
| `libs/components/vercel` | Vercel team platform, per-app project (`staging` CustomEnvironment + domains + bypass), WebApp, and Blob storage | `Platform`, `Project`, `WebApp`, `BlobStore` |
| `libs/components/neon` | Neon org defaults, per-app Neon Project cluster, and branched serverless PostgreSQL | `Platform`, `Project`, `Database` |
| `libs/components/kinde` | Two-tenant Kinde org connections, per-app OIDC `Application` pairs, and environment routing | `Platform`, `Project`, `OidcApp` |
| `libs/components/composite` | Turnkey org `Platform`, Premise `infra/shared` `*Project`, and `infra/` per-environment `*App` blueprints | `Platform`, `VercelNeonProject`, `VercelNeonApp`, `GcpCloudRunNeonProject`, `GcpCloudRunNeonApp` |
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

Formatting and lint tasks use the workspace's root `.golangci.yml`. `libs/components` is published (`package.public: true`) through the repository's shared `vX.Y.Z` Git tag via Premise.
