# Cloud Run + GCS + Neon + Kinde App (`gcp-neon-kinde-app`)

Consumer fixture declaring `composite.GcpCloudRunNeonApp` across `shared`, `dev`, `stage`, `prod`, and `preview-<slug>` stacks.

## Run E2E & Keep Live for Manual Testing

```sh
cd e2e/components

# Deploy shared -> dev -> preview-pr-12, verify all BDD assertions, and keep live
mise run e2e -- gcp_neon_kinde_app --no-cleanup

# Optional: step into the materialized workspace (pre-wired to its Pulumi state)
cd ../outputs/components/TestGCPNeonKindeApp/workspace
pulumi stack output --stack preview-pr-12

# Tear down all retained stacks when finished inspecting dashboards
mise run e2e:cleanup -- gcp_neon_kinde_app
```

## Endpoints

- `/` — Interactive HTML dashboard with Kinde OIDC Sign-In / Sign-Out, Neon PostgreSQL branch notes form, and GCS bucket overlay summary.
- `/api/health` — JSON health check verifying Neon SQL connectivity, GCS bucket overlay wiring, and Secret Manager-mounted Kinde OIDC configuration.
- `/api/auth/login`, `/api/auth/kinde_callback`, `/api/auth/logout` — Full OAuth2 authorization-code + HMAC session cookie flow against Kinde.
- `/api/notes` (`GET` / `POST`) — Reads and inserts rows in `app_notes` on the stack's Neon branch.
- `/api/storage?key=...` (`GET` / `POST`) — Writes to `STORAGE_BUCKET` and reads with automatic fallback to `STORAGE_FALLBACK_BUCKET`.
