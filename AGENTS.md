# Repository Guidance

## Hard Rules

- Prefer the repository's existing Mise tasks in [mise.toml](./mise.toml) and [libs/components/mise.toml](./libs/components/mise.toml).
- Manage monorepo projects and Go Git-tag releases through [premise.yaml](./premise.yaml).
- Keep `libs/components` as a pure Go library inside the single root [go.mod](./go.mod) with `GOWORK=off`, no nested `go.mod` files, and no committed local `replace` directives.
- Refresh Terraform-bridged Go bindings via `mise run generate` and verify zero drift with `mise run generate:check` ([scripts/generate-bridged.sh](./scripts/generate-bridged.sh)).
- Run live cloud end-to-end tests only against dedicated OpenBase cloud sandboxes (`cloudvoyant-openbase` on GCP and isolated `openbase-e2e-*` resources on Vercel, Neon, and Kinde); never use or mutate `neokit` projects.

## Organization

### Repository layout

- Place shared lifecycle contracts in `libs/components/core` and provider packages in `libs/components/{gcp,vercel,neon,kinde,synthetic}`. See [docs/architecture/index.md](./docs/architecture/index.md).
- Nest component end-to-end tests and consumer fixtures under [libs/components/e2e](./libs/components/e2e/README.md).

### File layout

- [Proposed] Group exported declarations under `// Public API` before unexported helpers under `// Private helpers` in handwritten Go files.

## Style

### Go

- Name component types after concrete cloud products (`gcp.CloudRunService`, `gcp.Bucket`, `vercel.WebApp`, `vercel.BlobStore`, `neon.Database`, `kinde.Auth`) with standard Go `New<Type>` constructors.
- [Proposed] Keep every handwritten function at or below cyclomatic complexity `10` as enforced by [.golangci.yml](./.golangci.yml).

### Pulumi

- Embed `pulumi.ResourceState` directly on concrete component structs alongside `core.StatelessMeta` or `core.StatefulMeta`.
- [Proposed] Gate `BranchFrom` on preview or child stacks so persistent root stacks do not reference themselves as a branch parent.

## Testing

- Co-locate fast offline `pulumi.WithMocks` unit tests (`*_test.go`) beside each component package and keep standalone consumer E2E suites in `libs/components/e2e`.
- Verify every component module live with `mise run e2e:live` (`//libs/components:e2e:live`) before release; skipped live suites never count as passing validation.
- [Proposed] Structure tests with nested `when ...` and `it ...` subtests and explicit `// Arrange.`, `// Act.`, and `// Assert.` comments.
