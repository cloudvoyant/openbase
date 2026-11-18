# OpenBase Go Pulumi components (`libs/components`)

Declarative Go Pulumi components for provisioning services, object storage, serverless PostgreSQL, and OIDC authentication across GCP, Vercel, Neon, and Kinde from one shared infrastructure program.

## Install

```sh
go get github.com/cloudvoyant/openbase/libs/components@v0.1.0
```

## Packages

| Package | Role | Declarative Specs |
| --- | --- | --- |
| `libs/components/core` | Lifecycle contracts, environment validation, naming, tags, and `Links` | `Stateless`, `Stateful`, `StatelessResource`, `StatefulResource`, `ResolveLinks` |
| `libs/components/gcp` | Google Cloud Platform compute, storage, and shared platform roots | `Shared`, `CloudRunService`, `Bucket`, `overlayfs` |
| `libs/components/vercel` | Vercel web hosting and Blob storage | `Shared`, `WebApp`, `BlobStore` |
| `libs/components/neon` | Branched serverless PostgreSQL | `SharedProject`, `Database` |
| `libs/components/kinde` | Two-tenant OAuth2 / OIDC authentication | `SharedOidcApp`, `OidcApp` |
| `libs/components/composite` | Premise-aligned `infra/shared` platform roots and `infra/` per-environment application blueprints | `VercelNeonShared`, `VercelNeonApp`, `GcpCloudRunNeonShared`, `GcpCloudRunNeonApp` |
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
