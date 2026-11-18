# Neokit-Style Vercel + Neon + Kinde App (`vercel-neon-kinde-app`)

Composite consumer fixture composing `vercel.Shared`, `vercel.BlobStore`, `vercel.WebApp`, `neon.SharedProject`, `neon.Database`, `kinde.SharedOidcApp`, and `kinde.OidcApp` via native Pulumi `StackReference`s.

## Run E2E & Keep Live for Manual Testing

```sh
cd e2e/components

# Deploy shared -> dev -> preview-pr-11, verify all BDD assertions, and keep live
mise run e2e -- vercel_neon_kinde_app --no-cleanup

# Optional: step into the materialized workspace (pre-wired to its Pulumi state)
cd ../outputs/components/TestVercelNeonKindeApp/workspace
pulumi stack output --stack preview-pr-11

# Tear down all retained stacks when finished inspecting dashboards
mise run e2e:cleanup -- vercel_neon_kinde_app
```

## Endpoints

- `/` — Interactive HTML dashboard with Kinde OIDC Sign-In / Sign-Out, Neon PostgreSQL branch notes form, and Vercel Blob prefix overlay summary.
- `/api/health` — JSON health check verifying Neon SQL connectivity, Vercel Blob overlay prefixes, and Kinde OIDC configuration.
- `/api/auth/login`, `/api/auth/kinde_callback`, `/api/auth/logout` — Full OAuth2 authorization-code + HMAC session cookie flow against Kinde.
- `/api/notes` (`GET` / `POST`) — Reads and inserts rows in `app_notes` on the stack's Neon branch.
- `/api/storage?key=...` (`GET` / `POST`) — Writes to `BLOB_PREFIX` and reads with automatic fallback to `BLOB_FALLBACK_PREFIX`.
