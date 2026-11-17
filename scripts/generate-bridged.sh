#!/usr/bin/env sh
# Generates root-module Go bindings for Terraform-bridged Pulumi providers (kislerdm/neon and kinde-oss/kinde)
# without nested go.mod files or filesystem replace directives.
set -eu

CHECK_MODE=0
if [ "${1:-}" = "--check" ]; then
    CHECK_MODE=1
fi

REPO_ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
MODULE_PATH="github.com/cloudvoyant/openbase"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_ROOT"' EXIT

generate_provider() {
    tf_source="$1"
    tf_version="$2"
    pkg_name="$3"

    schema_file="$TMP_ROOT/${pkg_name}-schema.json"
    sdk_out="$TMP_ROOT/${pkg_name}-sdk"
    dest_dir="$REPO_ROOT/internal/providers/${pkg_name}"

    if [ "$CHECK_MODE" = "1" ]; then
        dest_dir="$TMP_ROOT/check/internal/providers/${pkg_name}"
    fi

    echo "Generating bridged Go bindings for ${tf_source}@${tf_version} -> internal/providers/${pkg_name}..."
    PULUMI_SKIP_UPDATE_CHECK=true pulumi package get-schema terraform-provider "${tf_source}" "${tf_version}" >"$schema_file"

    python3 - "$schema_file" "${MODULE_PATH}/internal/providers/${pkg_name}" "${pkg_name}" <<'PY'
import json, sys
schema_path, import_base, pkg_name = sys.argv[1], sys.argv[2], sys.argv[3]
data = json.loads(open(schema_path, "r", encoding="utf-8").read())
data.get("functions", {}).pop(f"pulumi:providers:{pkg_name}/terraformConfig", None)
data.get("provider", {}).get("methods", {}).pop("terraformConfig", None)
lang = data.setdefault("language", {})
go_cfg = lang.setdefault("go", {})
go_cfg["importBasePath"] = import_base
open(schema_path, "w", encoding="utf-8").write(json.dumps(data, indent=2) + "\n")
PY

    PULUMI_SKIP_UPDATE_CHECK=true pulumi package gen-sdk "$schema_file" --language go --out "$sdk_out"

    src_pkg_dir="$sdk_out/go/${pkg_name}"
    if [ ! -d "$src_pkg_dir" ]; then
        echo "error: expected generated directory $src_pkg_dir not found" >&2
        ls -la "$sdk_out/go" >&2 || true
        exit 1
    fi

    rm -rf "$dest_dir"
    mkdir -p "$(dirname "$dest_dir")"
    cp -R "$src_pkg_dir" "$dest_dir"
    rm -f "$dest_dir/go.mod" "$dest_dir/go.sum"
}

generate_provider "kislerdm/neon" "0.18.0" "neon"
generate_provider "registry.terraform.io/kinde-oss/kinde" "0.1.1" "kinde"

if [ "$CHECK_MODE" = "1" ]; then
    diff -ruN "$REPO_ROOT/internal/providers/neon" "$TMP_ROOT/check/internal/providers/neon"
    diff -ruN "$REPO_ROOT/internal/providers/kinde" "$TMP_ROOT/check/internal/providers/kinde"
    echo "generate:check passed (no drift in internal/providers/{neon,kinde})."
fi
