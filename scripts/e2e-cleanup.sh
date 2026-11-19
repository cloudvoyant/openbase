#!/usr/bin/env sh
# Tear down any leftover or retained E2E stacks recorded under e2e/outputs/components/*/run-manifest.json.
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"
OUTPUTS_ROOT="$REPO_ROOT/e2e/outputs/components"

FILTER=""
for arg in "$@"; do
  case "$arg" in
    --) ;;
    -h|--help)
      echo "Usage: mise run e2e:cleanup [target]"
      echo "Destroys any retained or interrupted E2E stacks recorded in e2e/outputs/components/*/run-manifest.json."
      exit 0
      ;;
    *)
      FILTER="$arg"
      ;;
  esac
done

if [ ! -d "$OUTPUTS_ROOT" ]; then
  echo "No E2E output directory found ($OUTPUTS_ROOT); nothing to clean up."
  exit 0
fi

ENV_FILE="${OPENBASE_ENV_FILE:-$HOME/Projects/neokit/.env.bootstrap.consumed}"
if [ -z "${VERCEL_TOKEN:-}" ] && [ -z "${NEON_API_KEY:-}" ] && [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi
unset PROD_DOMAIN STAGE_DOMAIN VERCEL_PROJECT_ID NEON_PROJECT_ID
export OPENBASE_TEST_GCP_PROJECT="${OPENBASE_TEST_GCP_PROJECT:-cloudvoyant-openbase}"
export GOOGLE_PROJECT="$OPENBASE_TEST_GCP_PROJECT"
if [ -n "${VERCEL_TOKEN:-}" ]; then
  export VERCEL_API_TOKEN="$VERCEL_TOKEN"
fi

matches_filter() {
  target="$1"
  test_name="$2"
  fixture="$3"
  if [ -z "$target" ] || [ "$target" = "all" ]; then
    return 0
  fi
  norm_target="$(printf '%s' "$(basename "$target")" | sed 's/_test\.go$//; s/\.go$//' | tr '-' '_' | tr '[:upper:]' '[:lower:]')"
  norm_test="$(printf '%s' "$test_name" | tr '-' '_' | tr '[:upper:]' '[:lower:]')"
  norm_fix="$(printf '%s' "$fixture" | tr '-' '_' | tr '[:upper:]' '[:lower:]')"
  case "$norm_test" in
    *"$norm_target"*) return 0 ;;
  esac
  [ "$norm_fix" = "$norm_target" ]
}

purge_vercel_blobs() {
  vpid="$1"
  vtid="$2"
  if [ -z "$vpid" ] || [ -z "${VERCEL_TOKEN:-}" ]; then
    return 0
  fi
  team_q=""
  if [ -n "$vtid" ]; then
    team_q="?teamId=$vtid"
  fi
  env_id="$(curl -sf "https://api.vercel.com/v10/projects/${vpid}/env${team_q}" -H "Authorization: Bearer $VERCEL_TOKEN" 2>/dev/null | jq -r '.envs[] | select(.key=="BLOB_READ_WRITE_TOKEN" and (.target | index("development"))) | .id' | head -n1 || true)"
  if [ -z "$env_id" ]; then
    return 0
  fi
  rw_tok="$(curl -sf "https://api.vercel.com/v9/projects/${vpid}/env/${env_id}${team_q}" -H "Authorization: Bearer $VERCEL_TOKEN" 2>/dev/null | jq -r '.value // empty' || true)"
  if [ -z "$rw_tok" ]; then
    return 0
  fi
  urls="$(curl -sf "https://blob.vercel-storage.com?limit=1000" -H "Authorization: Bearer $rw_tok" -H "x-api-version: 7" 2>/dev/null | jq -c '{urls: [.blobs[]?.url]}' || true)"
  if [ -n "$urls" ] && [ "$urls" != '{"urls":[]}' ]; then
    curl -sf -X POST "https://blob.vercel-storage.com/delete" \
      -H "Authorization: Bearer $rw_tok" \
      -H "x-api-version: 7" \
      -H "Content-Type: application/json" \
      -d "$urls" >/dev/null || true
  fi
}

CLEANED_COUNT=0
FAILED=0

for manifest in "$OUTPUTS_ROOT"/*/run-manifest.json; do
  [ -f "$manifest" ] || continue
  CLEANED_UP="$(jq -r '.cleanedUp // false' "$manifest")"
  if [ "$CLEANED_UP" = "true" ]; then
    continue
  fi

  TEST_NAME="$(jq -r '.testName' "$manifest")"
  FIXTURE="$(jq -r '.fixture' "$manifest")"
  if ! matches_filter "$FILTER" "$TEST_NAME" "$FIXTURE"; then
    continue
  fi

  WORKSPACE_DIR="$(jq -r '.workspaceDir' "$manifest")"
  PULUMI_HOME_DIR="$(jq -r '.pulumiHome' "$manifest")"
  BACKEND_URL="$(jq -r '.backendUrl' "$manifest")"
  VPID="$(jq -r '.vercelProjectId // empty' "$manifest")"
  VTID="$(jq -r '.vercelTeamId // empty' "$manifest")"

  if [ ! -d "$WORKSPACE_DIR" ]; then
    echo "Skipping $TEST_NAME: workspace directory missing ($WORKSPACE_DIR)" >&2
    continue
  fi

  echo "=== Cleaning up retained E2E stacks for $TEST_NAME ($FIXTURE) ==="
  purge_vercel_blobs "$VPID" "$VTID"

  SUITE_OK=1
  STACKS_REV="$(jq -r '.stacks // [] | reverse[]' "$manifest")"
  for stack in $STACKS_REV; do
    echo "  -> Destroying stack '$stack'..."
    if ! (
      export GOWORK="off"
      export PULUMI_HOME="$PULUMI_HOME_DIR"
      export PULUMI_BACKEND_URL="$BACKEND_URL"
      export PULUMI_CONFIG_PASSPHRASE=""
      export PULUMI_SKIP_UPDATE_CHECK="true"
      eval "$(jq -r '(.env // {}) | to_entries[] | "export \(.key)=\(.value | @sh)"' "$manifest")"
      STACK_DIR="$WORKSPACE_DIR"
      if [ "$stack" = "shared" ] && [ -f "$WORKSPACE_DIR/infra/shared/Pulumi.yaml" ]; then
        STACK_DIR="$WORKSPACE_DIR/infra/shared"
      elif [ -f "$WORKSPACE_DIR/infra/Pulumi.yaml" ]; then
        STACK_DIR="$WORKSPACE_DIR/infra"
      fi
      cd "$STACK_DIR"
      pulumi login "$BACKEND_URL" >/dev/null
      if pulumi stack ls --json 2>/dev/null | jq -e --arg s "$stack" '.[] | select(.name == $s)' >/dev/null 2>&1; then
        BKT="$(pulumi stack output bucketName --stack "$stack" 2>/dev/null || true)"
        if [ -n "$BKT" ] && command -v gcloud >/dev/null 2>&1; then
          gcloud storage rm --all-versions -r "gs://${BKT}/**" >/dev/null 2>&1 || true
        fi
        pulumi state unprotect --all --yes --stack "$stack" >/dev/null 2>&1 || true
        pulumi destroy --stack "$stack" --yes --non-interactive --skip-preview &&
          pulumi stack rm "$stack" --yes --non-interactive >/dev/null 2>&1
      fi
    ); then
      echo "  !! Failed to destroy stack '$stack' in $TEST_NAME" >&2
      SUITE_OK=0
      FAILED=1
      break
    fi
  done

  if [ "$SUITE_OK" = "1" ]; then
    NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    TMP_MANIFEST="${manifest}.tmp"
    jq --arg now "$NOW" '.cleanedUp = true | .updatedAt = $now' "$manifest" >"$TMP_MANIFEST"
    mv "$TMP_MANIFEST" "$manifest"
    CLEANED_COUNT=$((CLEANED_COUNT + 1))
    echo "=== Cleaned $TEST_NAME ==="
  fi
done

if [ "$FAILED" != "0" ]; then
  exit 1
fi
if [ "$CLEANED_COUNT" = "0" ]; then
  echo "No leftover E2E stacks to clean up."
fi
