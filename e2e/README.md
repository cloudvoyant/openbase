# End-to-end specifications

Top-level `e2e/` organizes OpenBase end-to-end suites by target, with `e2e/components` testing `libs/components` (`package e2e_test`, no build tags). Suites follow `difflab-bdd`: each `Test*` mirrors its behavior flowchart through nested `given <context>` -> `when <action>` -> `then <outcome>` subtests (collapsing single-outcome branches to `when <action>, then <outcome>`), hoists shared Pulumi stack setup to enclosing `given` converged-state scopes, and stores materialized workspaces, Pulumi state, secret-redacted CLI transcripts (`commands.log`), and JSON snapshots under `e2e/outputs/components/<Test>/` (git-ignored).

| File (`e2e/components`) | Target Name | Behavior |
| --- | --- | --- |
| `core_test.go` | `core` | Offline standalone consumer preview/apply for `core.Stateful` & `core.Stateless`, fail-fast invalid-environment and `InvalidBranchSource` rejection, and artifact capture |
| `gcp_bucket_test.go` | `gcp_bucket` | `pulumi preview`, converged `dev`/`stage`/`prod` apply, in-place label updates, backend state persistence & fresh-checkout query, `Protect` guard, apply-failure preservation, `overlayfs` `preview<-dev` isolation, and idempotent `404` destruction |
| `gcp_cloud_run_test.go` | `gcp_cloud_run` | `gcp.Project` setup, `6–30` char SA bounds on long preview slugs, in-place revision updates, `public` (`200`) vs `internal` (`403` unauthenticated / `200` IAM ID token + linked service-to-service), Secret Manager mounts, keyless GCS v4 signed URLs, and idempotent `404` destruction |
| `neon_database_test.go` | `neon_database` | `neon.Project`, `dev`/`prod`/`stage<-prod` branches, `Protect` guard, `preview<-dev` CoW SQL isolation (`pgx`), and idempotent `404` branch destruction |
| `kinde_oidc_app_test.go` | `kinde_oidc_app` | Two-tenant `kinde.Project` (`dev` + `prod`), in-place callback updates, `email:password` connection, tier routing, live `/oauth2/auth` allowed (`302`) vs rejected redirects, and safe teardown |
| `vercel_blob_store_test.go` | `vercel_blob_store` | `vercel.Project` + `BlobStore`, `stage` CustomEnvironment token propagation, `preview<-dev` `BlobOverlayClient` prefix isolation, and cleanup |
| `vercel_web_app_test.go` | `vercel_web_app` | Squat-proof `<project>-<team>.vercel.app` domains/aliases, target-scoped env vars, `x-vercel-protection-bypass` (`401`/`403`/`302` vs `200`), in-place revision updates, and `404` `Alias`/`Deployment` cleanup |
| `links_test.go` | `links` | Cross-cloud `vercel.WebApp` -> keyless GCP WIF OIDC -> internal `gcp.CloudRunService` + `gcp.Bucket` overlay + `neon.Database` CoW + `kinde.OidcApp` secrets, plus multi-cloud `404` preview teardown |
| `vercel_neon_kinde_app_test.go` | `vercel_neon_kinde_app` | Premise-structured `composite.VercelNeonProject` (`infra/shared`) + `composite.VercelNeonApp` (`infra`) across `shared`, `dev`, `prod`, `stage<-prod`, and `preview-pr-11<-dev`, testing HTML `/`, `/api/health`, `/api/notes`, `/api/storage`, `prod` + `stage` Neon CoW & `prod` Kinde routing, `preview` overlay isolation, headless Chrome Kinde Email + OTP login/logout, and `404` cleanup |
| `gcp_neon_kinde_app_test.go` | `gcp_neon_kinde_app` | Premise-structured `composite.GcpCloudRunNeonProject` (`infra/shared`) + `composite.GcpCloudRunNeonApp` (`infra`) across `shared`, `dev`, `prod`, `stage<-prod`, and `preview-pr-12<-dev`, testing HTML `/`, `/api/health`, `/api/notes`, `/api/storage`, `prod` + `stage` Neon CoW & `prod` Kinde routing, `preview` GCS/Neon isolation, headless Chrome Kinde Email + OTP login/logout, and `404` cleanup |

## Running suites & retaining live apps for manual testing

From `e2e/components` (or the repository root), `mise run e2e` accepts an optional file/suite name (runs all by default) and `--no-cleanup` / `--keep` (or `OPENBASE_E2E_KEEP=1`) to retain deployed cloud resources and local Pulumi state under `e2e/outputs/components/<Test>/`:

```sh
cd e2e/components

# Run offline core lifecycle suite (no cloud credentials required)
mise run e2e -- core

# Run all live E2E suites and clean up automatically
mise run e2e

# Run a single composite app E2E and keep dev + prod + stage + preview-* live for manual testing
mise run e2e -- vercel_neon_kinde_app --no-cleanup
mise run e2e -- gcp_neon_kinde_app --no-cleanup
# (or via env var: OPENBASE_E2E_KEEP=1 mise run e2e -- vercel_neon_kinde_app)

# Step into a retained materialized Premise workspace to inspect with pm or pulumi directly
cd ../outputs/components/TestVercelNeonKindeApp/workspace
pm infra output --shared
pm infra output --env stage
cd infra && pulumi stack ls && pulumi stack output --stack preview-pr-11

# Tear down all retained or interrupted E2E stacks (or pass a specific target)
mise run e2e:cleanup
mise run e2e:cleanup -- vercel_neon_kinde_app
```

## Sandbox credentials

Live suites store Pulumi filestate in `e2e/outputs/components/<Test>/state` by default (override with `PULUMI_BACKEND_URL=gs://<bucket>/<prefix>` if desired) and target dedicated OpenBase sandboxes:

| Variable | Suites | Purpose |
| --- | --- | --- |
| `OPENBASE_TEST_GCP_PROJECT` | GCP, Links, `gcp_neon_kinde_app` | Dedicated sandbox GCP project ID (defaults to `cloudvoyant-openbase`) |
| `NEON_API_KEY` | Neon, Links, composite apps | Neon API key (creates & deletes isolated per-run Neon project) |
| `KINDE_DEV_TENANT_DOMAIN`, `KINDE_DEV_M2M_CLIENT_ID`, `KINDE_DEV_M2M_CLIENT_SECRET`, `KINDE_PROD_TENANT_DOMAIN`, `KINDE_PROD_M2M_CLIENT_ID`, `KINDE_PROD_M2M_CLIENT_SECRET` | Kinde, Links, composite apps | Dev and prod Kinde M2M credentials |
| `VERCEL_TOKEN` | Vercel, Links, `vercel_neon_kinde_app` | Vercel API token (creates & deletes isolated per-run Vercel project) |
| `AGENTMAIL_API_KEY`, `E2E_P1_EMAIL` | `vercel_neon_kinde_app`, `gcp_neon_kinde_app` | AgentMail test inbox for end-to-end Kinde 6-digit OTP sign-in & sign-out |
| `OPENBASE_E2E_KEEP` | All live suites | Set `1` (or pass `--no-cleanup` / `--keep`) to retain stacks until `mise run e2e:cleanup` |
