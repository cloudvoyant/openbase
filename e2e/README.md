# End-to-end specifications

Top-level `e2e/` organizes OpenBase end-to-end suites by target, and `e2e/components` tests `libs/components` (`package e2e_test`, no build tags). Suites follow `difflab-bdd`: each `Test*` mirrors its behavior flowchart through nested `given <context>` -> `when <action>` -> `then <outcome>` subtests (collapsing single-outcome branches to `when <action>, then <outcome>`), hoists shared Pulumi stack setup to enclosing `given` converged-state scopes, and stores materialized workspaces, Pulumi state, secret-redacted CLI transcripts (`commands.log`), and JSON snapshots under `e2e/outputs/components/<Test>/` (git-ignored).

| File (`e2e/components`) | Target Name | Behavior |
| --- | --- | --- |
| `core_test.go` | `core` | Offline standalone consumer preview and apply (`infra/main.go`) for `core.Stateful` and `core.Stateless`, fail-fast invalid-environment and `InvalidBranchSource` rejection, and artifact capture |
| `gcp_neon_kinde_app_test.go` | `gcp`, `neon`, `gcp_neon_kinde_app` | Separate `openbase.Platform` (`platform/main.go`), `openbase.Project` (`infra/shared/main.go`), and `openbase.Environment` (`infra/main.go`) across `platform`, `shared`, `dev`, `prod`, `stage<-prod`, and `preview-pr-12<-dev`, verifying GCS `Protect` and `SoftDeletePolicy`, `OPENBASE_TEST_FAIL_INFRA` preservation, fresh-checkout state query, `overlayfs.Store` isolation, keyless GCS v4 signed URLs, `pgx` Neon CoW SQL, Kinde Email and OTP login/logout, and `404` cleanup |
| `vercel_neon_kinde_app_test.go` | `vercel`, `kinde`, `vercel_neon_kinde_app` | Separate `openbase.Platform` (`platform/main.go`), `openbase.Project` (`infra/shared/main.go`), and `openbase.Environment` (`infra/main.go`) across `platform`, `shared`, `dev`, `prod`, `stage<-prod`, and `preview-pr-11<-dev`, verifying `stage` CustomEnvironment Blob propagation, `x-vercel-protection-bypass`, `BlobOverlayClient` prefix isolation, Neon CoW, Kinde unregistered redirect rejection and headless Chrome OTP login/logout, and `404` cleanup |
| `links_test.go` | `links` | Separate `openbase.Platform` (`platform/main.go`), `openbase.Project` (`infra/shared/main.go` with `shared-assets` bucket in `openbaseManifest`), and `openbase.Environment` (`infra/main.go`) verifying cross-cloud `vercel.WebApp` -> `gcp.FederatedIdentity` keyless GCP OIDC -> internal `gcp.CloudRunService` (`403` vs `200`), raw Pulumi `secretmanager.Secret` + `SecretIamMember` wired alongside OpenBase components, `gcp.Bucket` overlay, remote `shared-assets` bucket access, and `404` preview teardown |

## Running suites and retaining live apps

From `e2e/components` (or the repository root), run `mise run e2e` with an optional suite name (all suites run by default). Pass `--no-cleanup` or `--keep` (or set `OPENBASE_E2E_KEEP=1`) to retain deployed cloud resources and local Pulumi state under `e2e/outputs/components/<Test>/`:

```sh
cd e2e/components

# Run offline core lifecycle suite without cloud credentials
mise run e2e -- core

# Run all live E2E suites and clean up automatically
mise run e2e

# Run a single full-stack app E2E and keep dev, prod, stage, and preview-* live
mise run e2e -- vercel_neon_kinde_app --no-cleanup
mise run e2e -- gcp_neon_kinde_app --no-cleanup

# Step into a retained materialized Premise workspace to inspect with pm or pulumi
cd ../outputs/components/TestVercelNeonKindeApp/workspace
pm infra output --shared
pm infra output --env stage
cd infra && pulumi stack ls && pulumi stack output --stack preview-pr-11

# Tear down all retained or interrupted E2E stacks (or pass a specific target)
mise run e2e:cleanup
mise run e2e:cleanup -- vercel_neon_kinde_app
```

## Sandbox credentials

Live suites store Pulumi filestate in `e2e/outputs/components/<Test>/state` by default (override with `PULUMI_BACKEND_URL=gs://<bucket>/<prefix>` when needed) and target dedicated OpenBase sandboxes:

| Variable | Suites | Purpose |
| --- | --- | --- |
| `OPENBASE_TEST_GCP_PROJECT` | GCP, Links, `gcp_neon_kinde_app` | Dedicated sandbox GCP project ID (defaults to `cloudvoyant-openbase`) |
| `NEON_API_KEY` | Neon, Links, full-stack apps | Neon API key (creates and deletes isolated per-run Neon project) |
| `KINDE_NONPROD_TENANT_DOMAIN`, `KINDE_NONPROD_M2M_CLIENT_ID`, `KINDE_NONPROD_M2M_CLIENT_SECRET`, `KINDE_PROD_TENANT_DOMAIN`, `KINDE_PROD_M2M_CLIENT_ID`, `KINDE_PROD_M2M_CLIENT_SECRET` | Kinde, Links, full-stack apps | NonProd and Prod Kinde M2M credentials (falls back to `KINDE_DEV_*` if `KINDE_NONPROD_*` is unset locally) |
| `VERCEL_TOKEN` | Vercel, Links, `vercel_neon_kinde_app` | Vercel API token (creates and deletes isolated per-run Vercel project) |
| `AGENTMAIL_API_KEY`, `E2E_P1_EMAIL` | `vercel_neon_kinde_app`, `gcp_neon_kinde_app` | AgentMail test inbox for end-to-end Kinde 6-digit OTP sign-in and sign-out |
| `OPENBASE_E2E_KEEP` | All live suites | Set `1` (or pass `--no-cleanup` or `--keep`) to retain stacks until `mise run e2e:cleanup` |
