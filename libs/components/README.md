# components

A native Go Pulumi component library with a Go consumer and mock tests.

## Use

From another Go Pulumi project, fetch a released package:

```bash
go get github.com/cloudvoyant/openbase/libs/components@v1.2.3
```

Import `github.com/cloudvoyant/openbase/libs/components` and call `components.NewGreeting`. See `examples/consumer` for a complete Go consumer. Mock tests do not deploy cloud resources or require cloud credentials.

## Develop

```bash
mise run install
mise run format:check
mise run lint
mise run test
mise run build
```

Formatting and lint tasks use the workspace's root `.golangci.yml`.

## Workspace

This project is a public library package (`package.public: true`) in the workspace's root Go module (`github.com/cloudvoyant/openbase`). Applications live under `apps/`, and libraries live under `libs/`. All packages share dependencies and one repository tag.
