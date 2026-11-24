# Repository Guidance

## Hard Rules

- Prefer the repository Mise tasks in [mise.toml](./mise.toml), [libs/components/mise.toml](./libs/components/mise.toml), and [e2e/components/mise.toml](./e2e/components/mise.toml).
- Manage monorepo projects and Go Git-tag releases through [premise.yaml](./premise.yaml).
- Keep `libs/components` as a pure Go library inside the single root [go.mod](./go.mod) with `GOWORK=off`, no nested `go.mod` files, and no committed local `replace` directives.
- Refresh Terraform-bridged Go bindings with `mise run generate` and make sure that `mise run generate:check` reports zero drift ([libs/internal/pulumi-neon-go-sdk/mise.toml](./libs/internal/pulumi-neon-go-sdk/mise.toml), [libs/internal/pulumi-kinde-go-sdk/mise.toml](./libs/internal/pulumi-kinde-go-sdk/mise.toml)).
- Run live cloud end-to-end tests only against dedicated OpenBase cloud sandboxes (`cloudvoyant-openbase` on GCP and isolated `openbase-e2e-*` resources on Vercel, Neon, and Kinde). Never use or mutate `neokit` projects.

## Organization

### Repository layout

- Place cross-stack lookup helpers (`LookupPlatform` and `LookupShared`) in the root `libs/components` package, shared lifecycle contracts in `libs/components/core`, and provider packages in `libs/components/{gcp,vercel,neon,kinde,synthetic}`. See [docs/architecture/index.md](./docs/architecture/index.md).
- Keep top-level `e2e/` organized by target suite, with component end-to-end suites and consumer fixtures under [e2e/components](./e2e/README.md).

### File layout

- [Proposed] Group exported declarations under `// Public API` before unexported helpers under `// Private helpers` in handwritten Go files.

## Style

### Go

- Follow standard Pulumi Go constructors (`New<Resource>(ctx, name, &<Resource>Args{...}, opts...) (*<Resource>, error)`) and typed lookups (`<provider>.LookupAccount(plat, name...)` and `<provider>.LookupProject(shared, name...)`) across organization accounts (`<provider>.NewAccount` in `infra/platform/main.go` on a dedicated `platform` stack), shared project resources (`<provider>.NewProject` in `infra/shared/main.go` on `shared`, omitting `env` from canonical naming), and environment resources (concrete products in `infra/env/main.go` across `dev`, `stage`, `prod`, and `preview-<slug>`).
- [Proposed] Document every exported function, type, and struct field in handwritten Go packages with a short comment explaining its purpose and cloud-provider mapping.
- [Proposed] Keep every handwritten function at or below cyclomatic complexity `10` as enforced by [.golangci.yml](./.golangci.yml).

### Pulumi

- Embed `pulumi.ResourceState` directly on concrete component structs alongside `core.AccountMeta`, `core.StatelessMeta`, or `core.StatefulMeta`.
- [Proposed] Gate `BranchFrom` on preview or child stacks so persistent root stacks do not reference themselves as a branch parent.

## Testing

- Co-locate fast offline `pulumi.WithMocks` integration and unit tests (`*_test.go`) beside each component package and keep standalone consumer E2E suites in `e2e/components`.
- Make sure that every component module passes live against cloud sandboxes with `mise run e2e:live` (`//e2e/components:e2e:live`) before release. Skipped live suites do not count as passing validation.
- [Proposed] Follow `difflab-bdd` across all Go test suites: mirror behavior flowcharts with `given <context>` -> `when <action>` -> `then <outcome>` subtests (collapsing single-outcome branches to `when <action>, then <outcome>`), hoist shared setup to enclosing `given` converged-state scopes, keep `// Arrange.`, `// Act.`, and `// Assert.` explicit, never test fixtures or harnesses themselves, and emit secret-redacted transcripts and JSON snapshots under `e2e/outputs/components/`.
