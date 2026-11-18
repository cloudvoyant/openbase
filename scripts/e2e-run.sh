#!/usr/bin/env sh
# Run OpenBase component E2E suites (all or a single file/target) with optional --no-cleanup / --keep.
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"
SUITE_DIR="$REPO_ROOT/e2e/components"

KEEP="${OPENBASE_E2E_KEEP:-${OPENBASE_TEST_KEEP:-0}}"
OFFLINE="${OPENBASE_E2E_OFFLINE:-0}"
TARGET=""

print_help() {
  cat <<'EOF'
Usage: mise run e2e [target] [--no-cleanup | --keep] [--cleanup] [--offline]

Targets (optional; omit to run all):
  core                     core_test.go (offline lifecycle & contract checks)
  gcp_bucket               gcp_bucket_test.go
  gcp_cloud_run            gcp_cloud_run_test.go
  gcp_neon_kinde_app       gcp_neon_kinde_app_test.go (Cloud Run + GCS + Neon + Kinde app)
  kinde_oidc_app           kinde_oidc_app_test.go
  links                    links_test.go
  neon_database            neon_database_test.go
  vercel_blob_store        vercel_blob_store_test.go
  vercel_neon_kinde_app    vercel_neon_kinde_app_test.go (Vercel + Blob + Neon + Kinde app)
  vercel_web_app           vercel_web_app_test.go

Flags:
  --no-cleanup, -k, --keep Keep cloud stacks & Pulumi state in e2e/outputs/components/<Test>/
                           (sets OPENBASE_E2E_KEEP=1; clean up later with `mise run e2e:cleanup`)
  --cleanup                Destroy stacks after the test completes (default)
  --offline                Run only offline E2E specs without cloud credentials
EOF
}

for arg in "$@"; do
  case "$arg" in
    --) ;;
    -h|--help)
      print_help
      exit 0
      ;;
    --no-cleanup|--cleanup=false|-k|--keep)
      KEEP="1"
      ;;
    --cleanup|--cleanup=true)
      KEEP="0"
      ;;
    --offline)
      OFFLINE="1"
      ;;
    -*)
      echo "Unknown flag: $arg" >&2
      print_help >&2
      exit 1
      ;;
    *)
      if [ -n "$TARGET" ]; then
        echo "Unexpected extra argument: $arg (target already set to '$TARGET')" >&2
        exit 1
      fi
      TARGET="$arg"
      ;;
  esac
done

NEEDS_LIVE="1"
RUN_REGEX=""

if [ -z "$TARGET" ] || [ "$TARGET" = "all" ]; then
  if [ "$OFFLINE" = "1" ]; then
    RUN_REGEX='^TestCoreLifecycle$'
    NEEDS_LIVE="0"
  fi
else
  BASE="$(basename "$TARGET")"
  BASE="${BASE%_test.go}"
  BASE="${BASE%.go}"
  NORM="$(printf '%s' "$BASE" | tr '-' '_')"
  case "$NORM" in
    core|core_lifecycle)
      RUN_REGEX='^TestCoreLifecycle$'
      NEEDS_LIVE="0"
      ;;
    gcp_bucket)
      RUN_REGEX='^TestGCPBucket$'
      ;;
    gcp_cloud_run|gcp_cloud_run_service)
      RUN_REGEX='^TestGCPCloudRunService$'
      ;;
    gcp_neon_kinde_app|gcp_app)
      RUN_REGEX='^TestGCPNeonKindeApp$'
      ;;
    vercel_neon_kinde_app|vercel_app)
      RUN_REGEX='^TestVercelNeonKindeApp$'
      ;;
    vercel_blob_store|vercel_blob)
      RUN_REGEX='^TestVercelBlobStore$'
      ;;
    vercel_web_app|vercel_webapp)
      RUN_REGEX='^TestVercelWebApp$'
      ;;
    neon_database|neon_db|neon)
      RUN_REGEX='^TestNeonDatabase$'
      ;;
    kinde_oidc_app|kinde_oidc|kinde)
      RUN_REGEX='^TestKindeOidcApp$'
      ;;
    links|links_fullstack)
      RUN_REGEX='^TestLinks$'
      ;;
    gcp)
      RUN_REGEX='^TestGCP(Bucket|CloudRunService)$'
      ;;
    vercel)
      RUN_REGEX='^TestVercel(BlobStore|WebApp)$'
      ;;
    apps)
      RUN_REGEX='^Test(VercelNeonKindeApp|GCPNeonKindeApp)$'
      ;;
    Test*)
      RUN_REGEX="^${BASE}$"
      ;;
    *)
      echo "Unknown E2E target: '$TARGET'" >&2
      print_help >&2
      exit 1
      ;;
  esac
fi

if [ "$OFFLINE" = "1" ]; then
  NEEDS_LIVE="0"
fi

if [ "$NEEDS_LIVE" = "1" ]; then
  ENV_FILE="${OPENBASE_ENV_FILE:-$HOME/Projects/neokit/.env.bootstrap.consumed}"
  if [ -z "${VERCEL_TOKEN:-}" ] && [ -z "${NEON_API_KEY:-}" ] && [ -f "$ENV_FILE" ]; then
    set -a
    # shellcheck disable=SC1090
    . "$ENV_FILE"
    set +a
  fi
  unset PROD_DOMAIN STAGE_DOMAIN VERCEL_PROJECT_ID NEON_PROJECT_ID
  export OPENBASE_TEST_GCP_PROJECT="${OPENBASE_TEST_GCP_PROJECT:-cloudvoyant-openbase}"
  export OPENBASE_TEST_LIVE="1"
fi

if [ "$KEEP" = "1" ]; then
  export OPENBASE_E2E_KEEP="1"
else
  unset OPENBASE_E2E_KEEP OPENBASE_TEST_KEEP
fi

export GOWORK="off"
cd "$SUITE_DIR"
if [ -n "$RUN_REGEX" ]; then
  exec go test . -run "$RUN_REGEX" -count=1 -timeout 60m -v
fi
exec go test . -count=1 -timeout 60m -v
