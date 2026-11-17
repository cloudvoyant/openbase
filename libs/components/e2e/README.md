# End-to-end tests

The `libs/components/e2e` package follows Premise's nested Go subtest pattern (`package e2e_test`, no build tags). Each top-level `Test*` function names the module under test, each `when ...` subtest arranges a standalone Go Pulumi consumer fixture in `t.TempDir()` and runs real `pulumi` CLI commands against an isolated `PULUMI_HOME` and per-run `gs://` `PULUMI_BACKEND_URL` prefix, and its `it ...` children verify cloud and remote-state outcomes.

| Suite | Contexts |
| --- | --- |
| `TestSyntheticConsumerFixture` | Offline standalone consumer preview and invalid-environment rejection |
| `TestGCPBucket` | Planning, applying/updating across `dev`/`stage`/`prod`/`preview` (`a -> b`), failure preservation (`OPENBASE_TEST_FAIL_INFRA=1`), fresh-checkout remote `gs://` state query, `overlayfs` preview branching, `Protect` guard, and idempotent destruction (`404`) |
| `TestGCPCloudRunService` | `gcp.Shared` setup, `6–30` char SA bounds on long preview slugs, in-place revision updates, `public` (`200`) vs `internal` (`403` unauthenticated / `200` IAM ID token + linked service-to-service), Secret Manager mounts, keyless GCS v4 signed URLs, and idempotent destruction (`404`) |
| `TestNeonDatabase` | `SharedProject`, `dev`/`prod`/`stage<-prod`, `Protect` guard, `preview<-dev` CoW SQL isolation (`pgx`), and branch destruction |
| `TestKindeAuth` | Two-tenant `SharedAuth` (`dev` + `prod`), `email:password` connection, OIDC discovery, allowed/rejected `/oauth2/auth` redirects, and safe teardown |
| `TestVercelBlobStore` | `BlobStore`, `stage` CustomEnvironment token propagation, `preview<-dev` prefix overlay, and cleanup |
| `TestVercelWebApp` | Team-safe domains/aliases, `x-vercel-protection-bypass` (`401`/`403` vs `200`), scoped env vars, and preview `Alias`/`Deployment` cleanup |
| `TestLinks` | Cross-cloud `vercel.WebApp` -> internal `gcp.CloudRunService` + `gcp.Bucket` OIDC federation + `neon.Database` + `kinde.Auth` links |

## Local fixture test

```sh
mise run e2e
```

Runs `TestSyntheticConsumerFixture` offline without cloud credentials. Live suites skip during default `go test ./...` runs unless their `OPENBASE_TEST_*` gate is set; a skipped test never satisfies live validation.

## Live infrastructure tests

Set `PULUMI_BACKEND_URL` to an existing `gs://` state bucket prefix and provide dedicated sandbox credentials:

| Variable | Suites | Purpose |
| --- | --- | --- |
| `PULUMI_BACKEND_URL` | All live suites | Existing `gs://<bucket>/<prefix>` Pulumi state backend |
| `OPENBASE_TEST_GCP_PROJECT` | `e2e:gcp`, `e2e:live` | Dedicated sandbox GCP project ID |
| `NEON_API_KEY` | `e2e:neon`, `e2e:live` | Neon API key (creates & deletes isolated per-run Neon project) |
| `KINDE_DEV_TENANT_DOMAIN`, `KINDE_DEV_M2M_CLIENT_ID`, `KINDE_DEV_M2M_CLIENT_SECRET`, `KINDE_PROD_TENANT_DOMAIN`, `KINDE_PROD_M2M_CLIENT_ID`, `KINDE_PROD_M2M_CLIENT_SECRET` | `e2e:kinde`, `e2e:live` | Dev and prod Kinde M2M credentials (creates & deletes isolated per-run Kinde apps) |
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

Use `-run` to target a single module or scenario during development:

```sh
OPENBASE_TEST_GCP=1 go test ./libs/components/e2e -run '^TestGCPBucket/when_branching' -count=1 -v
```
