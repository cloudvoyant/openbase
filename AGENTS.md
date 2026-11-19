# Repository Guidance

## Hard Rules

- Prefer the repository's existing Mise tasks in [mise.toml](./mise.toml), [libs/components/mise.toml](./libs/components/mise.toml), and [e2e/components/mise.toml](./e2e/components/mise.toml).
- Manage monorepo projects and Go Git-tag releases through [premise.yaml](./premise.yaml).
- Keep `libs/components` as a pure Go library inside the single root [go.mod](./go.mod) with `GOWORK=off`, no nested `go.mod` files, and no committed local `replace` directives.
- Refresh Terraform-bridged Go bindings via `mise run generate` and verify zero drift with `mise run generate:check` ([libs/internal/pulumi-neon-go-sdk/mise.toml](./libs/internal/pulumi-neon-go-sdk/mise.toml), [libs/internal/pulumi-kinde-go-sdk/mise.toml](./libs/internal/pulumi-kinde-go-sdk/mise.toml)).
- Run live cloud end-to-end tests only against dedicated OpenBase cloud sandboxes (`cloudvoyant-openbase` on GCP and isolated `openbase-e2e-*` resources on Vercel, Neon, and Kinde); never use or mutate `neokit` projects.

## Organization

### Repository layout

- Place shared lifecycle contracts in `libs/components/core`, provider packages in `libs/components/{gcp,vercel,neon,kinde,synthetic}`, and opinionated multi-provider application blueprints in `libs/components/composite`. See [docs/architecture/index.md](./docs/architecture/index.md).
- Keep top-level `e2e/` organized by target suite, with component end-to-end suites and consumer fixtures under [e2e/components](./e2e/README.md).

### File layout

- [Proposed] Group exported declarations under `// Public API` before unexported helpers under `// Private helpers` in handwritten Go files.

## Style

### Go

- Separate organization `Platform` specs (`core.PlatformResource`: `<provider>.Platform`, `composite.Platform`) from `Resource` specs (`core.StatelessResource` and `core.StatefulResource`: `<provider>.Project`, `composite.*Project`, and concrete products across `shared`, `dev`, `stage`, `prod`, and `preview-<slug>`, where `shared` omits `env` from canonical naming).
- [Proposed] Keep every handwritten function at or below cyclomatic complexity `10` as enforced by [.golangci.yml](./.golangci.yml).

### Pulumi

- Embed `pulumi.ResourceState` directly on concrete `*Resource` structs alongside `core.PlatformMeta`, `core.StatelessMeta`, or `core.StatefulMeta`.
- [Proposed] Gate `BranchFrom` on preview or child stacks so persistent root stacks do not reference themselves as a branch parent.

## Testing

- Co-locate fast offline `pulumi.WithMocks` integration/unit tests (`*_test.go`) beside each component package and keep standalone consumer E2E suites in `e2e/components`.
- Verify every component module live with `mise run e2e:live` (`//e2e/components:e2e:live`) before release; skipped live suites never count as passing validation.
- [Proposed] Follow `difflab-bdd` across all Go test suites: mirror behavior flowcharts with `given <context>` -> `when <action>` -> `then <outcome>` subtests (collapsing single-outcome branches to `when <action>, then <outcome>`), hoist shared setup to enclosing `given` converged-state scopes, keep `// Arrange.`, `// Act.`, and `// Assert.` explicit, never test fixtures/harnesses themselves, and emit secret-redacted transcripts and JSON snapshots under `e2e/outputs/components/`.
