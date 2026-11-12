#!/usr/bin/env bash
# install.sh — Install based binary
# Usage: curl -fsSL https://raw.githubusercontent.com/cloudvoyant/openbase/main/install.sh | bash
#
# Set VERSION=vX.Y.Z to pin a release; defaults to the latest.
# Set INSTALL_DIR to override the install location.
# Set OB_ALIAS=0 to skip installing the `ob` alias symlink (enabled by default).
#
# Prebuilt binaries cover linux/macOS on x86_64 and aarch64. To build from source:
#   go install github.com/cloudvoyant/openbase/apps/based@vX.Y.Z
set -euo pipefail

PROJECT="based"
GITHUB_ORG="cloudvoyant"
REPO="$GITHUB_ORG/openbase"
OB_ALIAS="${OB_ALIAS:-1}"

# Detect OS and arch
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64) ARCH="x86_64" ;;
    aarch64 | arm64) ARCH="aarch64" ;;
    *)
        echo "Unsupported architecture: $ARCH" >&2
        exit 1
        ;;
esac

case "$OS" in
    linux) TARGET="${ARCH}-linux" ;;
    darwin) TARGET="${ARCH}-macos" ;;
    *)
        echo "Unsupported OS: $OS" >&2
        exit 1
        ;;
esac

# Determine install directory
if [[ -n "${INSTALL_DIR:-}" ]]; then
    mkdir -p "$INSTALL_DIR"
elif [[ $EUID -eq 0 ]]; then
    INSTALL_DIR="/usr/local/bin"
else
    INSTALL_DIR="$HOME/.local/bin"
    mkdir -p "$INSTALL_DIR"
fi

TMP_DIR=$(mktemp -d)
STAGED_BINARY="$INSTALL_DIR/.$PROJECT.tmp.$$"
cleanup() {
    rm -rf "$TMP_DIR"
    rm -f "$STAGED_BINARY"
}
trap cleanup EXIT

# Resolve release tag (VERSION pins it; otherwise use the latest release)
if [[ -n "${VERSION:-}" ]]; then
    TAG="$VERSION"
else
    echo "Fetching latest $PROJECT release..."
    LATEST_URL="${OPENBASE_RELEASE_API_URL:-https://api.github.com/repos/$REPO/releases/latest}"
    if ! curl -fsSL --retry 3 --retry-delay 2 --retry-all-errors "$LATEST_URL" -o "$TMP_DIR/release.json"; then
        echo "Could not fetch the latest $PROJECT release. Check GitHub access or pass VERSION=vX.Y.Z." >&2
        exit 1
    fi
    TAG=$(grep '"tag_name"' "$TMP_DIR/release.json" | sed 's/.*"tag_name": *"\(.*\)".*/\1/')
    if [[ -z "$TAG" ]]; then
        echo "GitHub release response has no tag_name." >&2
        exit 1
    fi
fi

ASSET_NAME="${PROJECT}-${TAG}-${TARGET}.tar.gz"
DOWNLOAD_URL="${OPENBASE_DOWNLOAD_BASE_URL:-https://github.com/$REPO/releases/download}/$TAG/$ASSET_NAME"

echo "Downloading $ASSET_NAME..."
curl -fL --retry 3 --retry-delay 2 --retry-all-errors --progress-bar "$DOWNLOAD_URL" -o "$TMP_DIR/$ASSET_NAME"
tar -xzf "$TMP_DIR/$ASSET_NAME" -C "$TMP_DIR"

cp "$TMP_DIR/$PROJECT" "$STAGED_BINARY"
chmod +x "$STAGED_BINARY"
mv -f "$STAGED_BINARY" "$INSTALL_DIR/$PROJECT"

echo "Installed $PROJECT $TAG to $INSTALL_DIR/$PROJECT"
if [[ "$OB_ALIAS" != "0" && "$OB_ALIAS" != "false" ]]; then
    ln -sf "$PROJECT" "$INSTALL_DIR/ob"
    echo "Installed ob alias to $INSTALL_DIR/ob"
fi
echo "Make sure $INSTALL_DIR is in your PATH."
