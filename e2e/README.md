# End-to-end specifications

Top-level `e2e/` organizes OpenBase end-to-end suites by target, with `e2e/components` testing `libs/components` (`package e2e_test`, no build tags). Suites follow `difflab-bdd`: each `Test*` mirrors its behavior flowchart through nested `given <context>` -> `when <action>` -> `then <outcome>` subtests (collapsing single-outcome branches to `when <action>, then <outcome>`), hoists shared Pulumi stack setup to enclosing `given` converged-state scopes, and writes secret-redacted CLI transcripts (`commands.log`) and JSON/plan snapshots under `e2e/outputs/components/<Test>/` (git-ignored).

| Suite (`e2e/components`) | Behavior |
| --- | --- |
| `TestCoreLifecycle` | Offline standalone consumer preview/apply for `core.Stateful` & `core.Stateless`, fail-fast invalid-environment and `InvalidBranchSource` rejection, and artifact capture |
| `TestGCPBucket` | `pulumi preview`, converged `dev`/`stage`/`prod` apply, in-place label updates, remote `gs://` state persistence & fresh-checkout query, `Protect` guard, apply-failure preservation, `overlayfs` `preview<-dev` isolation, and idempotent `404` destruction |
| `TestGCPCloudRunService` | `gcp.Shared` setup, `6–30` char SA bounds on long preview slugs, in-place revision updates, `public` (`200`) vs `internal` (`403` unauthenticated / `200` IAM ID token + linked service-to-service), Secret Manager mounts, keyless GCS v4 signed URLs, and idempotent `404` destruction |
| `TestNeonDatabase` | `SharedProject`, `dev`/`prod`/`stage<-prod` branches, `Protect` guard, `preview<-dev` CoW SQL isolation (`pgx`), and idempotent `404` branch destruction |
| `TestKindeOidcApp` | Two-tenant `SharedOidcApp` (`dev` + `prod`), in-place callback updates, `email:password` connection, tier routing, live `/oauth2/auth` allowed (`302`) vs rejected redirects, and safe teardown |
| `TestVercelBlobStore` | `Shared` + `BlobStore`, `stage` CustomEnvironment token propagation, `preview<-dev` `BlobOverlayClient` prefix isolation, and cleanup |
| `TestVercelWebApp` | Squat-proof `<project>-<team>.vercel.app` domains/aliases, target-scoped env vars, `x-vercel-protection-bypass` (`401`/`403`/`302` vs `200`), in-place revision updates, and `404` `Alias`/`Deployment` cleanup |
| `TestLinks` | Cross-cloud `vercel.WebApp` -> keyless GCP WIF OIDC -> internal `gcp.CloudRunService` + `gcp.Bucket` overlay + `neon.Database` CoW + `kinde.OidcApp` secrets, plus multi-cloud `404` preview teardown |

## Local CLI specification

```sh
mise run e2e
```

Runs `TestCoreLifecycle` (`//e2e/components:e2e`) offline via real `pulumi` CLI subprocesses without cloud credentials and writes `e2e/outputs/components/TestCoreLifecycle/{commands.log,preview-plan.txt,stack-outputs.json}`. Live suites skip during default `go test` runs unless their `OPENBASE_TEST_*` gate is set; a skipped test never satisfies live validation.

## Live cloud specifications

Set `PULUMI_BACKEND_URL` to an existing `gs://` state bucket prefix and provide dedicated sandbox credentials:

| Variable | Suites | Purpose |
| --- | --- | --- |
| `PULUMI_BACKEND_URL` | All live suites | Existing `gs://<bucket>/<prefix>` Pulumi state backend |
| `OPENBASE_TEST_GCP_PROJECT` | `e2e:gcp`, `e2e:live` | Dedicated sandbox GCP project ID |
| `NEON_API_KEY` | `e2e:neon`, `e2e:live` | Neon API key (creates & deletes isolated per-run Neon project) |
| `KINDE_DEV_TENANT_DOMAIN`, `KINDE_DEV_M2M_CLIENT_ID`, `KINDE_DEV_M2M_CLIENT_SECRET`, `KINDE_PROD_TENANT_DOMAIN`, `KINDE_PROD_M2M_CLIENT_ID`, `KINDE_PROD_M2M_CLIENT_SECRET` | `e2e:kinde`, `e2e:live` | Dev and prod Kinde M2M credentials |
| `VERCEL_TOKEN` | `e2e:vercel`, `e2e:live` | Vercel API token (creates & deletes isolated per-run Vercel project) |

```sh
export PULUMI_BACKEND_URL=gs://YOUR_STATE_BUCKET/openbase-e2e
export OPENBASE_TEST_GCP_PROJECT=YOUR_GCP_PROJECT

mise run e2e:gcp
mise run e2e:neon
mise run e2e:kinde
mise run e2e:vercel
mise run e2e:live
```

Inspect human-verifiable CLI transcripts and JSON snapshots under `e2e/outputs/components/<Test>/` after any run, or target a single module during development:

```sh
OPENBASE_TEST_GCP=1 go test ./e2e/components -run '^TestGCPBucket' -count=1 -v
```
