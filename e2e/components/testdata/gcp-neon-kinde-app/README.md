# Premise Cloud Run + GCS + Neon + Kinde Workspace (`gcp-neon-kinde-app`)

Premise-structured consumer workspace splitting platform roots (`infra/shared` -> `composite.GcpCloudRunNeonShared`) from per-environment deployments (`infra` -> `composite.GcpCloudRunNeonApp` across `dev`, `prod`, `stage`, and `preview-<slug>`).

## Layout

- `premise.yaml` — Declares `workspace.infra.directory: infra` and `workspace.infra.shared_directory: infra/shared`.
- `infra/shared/` (`openbase-gcp-neon-kinde-app-shared`) — Provisions shared GCP Artifact Registry/WIF, Neon Project, and two-tenant Kinde OIDC applications on stack `shared`.
- `infra/` (`openbase-gcp-neon-kinde-app`) — Provisions per-environment Neon branch (`stage<-prod`, `preview<-dev`), GCS bucket overlay, Secret Manager Kinde binding, and Cloud Run v2 service via `StackReference` (`organization/openbase-gcp-neon-kinde-app-shared/shared`).
- `infra/app/server.py` — Interactive Cloud Run HTTP dashboard (`/`, `/api/health`, `/api/auth/*`, `/api/notes`, `/api/storage`).

## Run E2E & Keep Live for Manual Testing

```sh
cd e2e/components

# Deploy shared -> dev -> prod -> stage -> preview-pr-12, verify all BDD assertions, and keep live
mise run e2e -- gcp_neon_kinde_app --no-cleanup

# Inspect retained Premise workspace with pm or pulumi
cd ../outputs/components/TestGCPNeonKindeApp/workspace
pm infra output --shared
pm infra output --env stage
(cd infra && pulumi stack ls)

# Tear down all retained stacks when finished inspecting dashboards
mise run e2e:cleanup -- gcp_neon_kinde_app
```
