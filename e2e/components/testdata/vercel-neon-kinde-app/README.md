# Premise Vercel + Neon + Kinde Workspace (`vercel-neon-kinde-app`)

Premise-structured consumer workspace splitting shared project assets (`infra/shared` -> `composite.VercelNeonProject`) from per-environment deployments (`infra` -> `composite.VercelNeonApp` across `dev`, `prod`, `stage`, and `preview-<slug>`), with application source in `apps/web`.

## Layout

- `premise.yaml` — Declares `workspace.infra.directory: infra` and `workspace.infra.shared_directory: infra/shared`.
- `infra/shared/` (`openbase-vercel-neon-kinde-app-shared`) — Provisions shared Vercel Project, Blob Store, shared `preview` env vars, Neon Project, and two-tenant Kinde OIDC applications on stack `shared`.
- `infra/` (`openbase-vercel-neon-kinde-app`) — Provisions per-environment Neon branch (`stage<-prod`, `preview<-dev`), Blob overlay, Kinde binding, and Vercel deployment/alias via `StackReference` (`organization/openbase-vercel-neon-kinde-app-shared/shared`).
- `apps/web/` — Interactive Node serverless dashboard (`/`, `/api/health`, `/api/auth/*`, `/api/notes`, `/api/storage`).

## Run E2E & Keep Live for Manual Testing

```sh
cd e2e/components

# Deploy shared -> dev -> prod -> stage -> preview-pr-11, verify all BDD assertions, and keep live
mise run e2e -- vercel_neon_kinde_app --no-cleanup

# Inspect retained Premise workspace with pm or pulumi
cd ../outputs/components/TestVercelNeonKindeApp/workspace
pm infra output --shared
pm infra output --env stage
(cd infra && pulumi stack ls)

# Tear down all retained stacks when finished inspecting dashboards
mise run e2e:cleanup -- vercel_neon_kinde_app
```
