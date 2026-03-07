#!/usr/bin/env bash

set -euo pipefail

echo "======================================"
echo "      Animasola Bundle Installer      "
echo "======================================"

INSTALL_ROOT="${ANIMASOLA_INSTALL_ROOT:-$HOME/.local/share/animasola/current}"
BIN_TARGET="${ANIMASOLA_BIN_TARGET:-$HOME/.local/bin/animasola}"
TMP_DIR="$(mktemp -d)"
INSTALL_PARENT="$(dirname "$INSTALL_ROOT")"
BIN_PARENT="$(dirname "$BIN_TARGET")"
BUNDLE_LISTING="$TMP_DIR/bundle-listing.txt"
BUNDLE_TYPES="$TMP_DIR/bundle-types.txt"
BUNDLE_ARCHIVE=""

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

detect_asset_name() {
    local os arch
    os="$(uname | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$arch" in
        x86_64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *)
            echo "ERROR: unsupported architecture: $arch"
            exit 1
            ;;
    esac

    case "$os" in
        linux|darwin)
            printf 'animasola-%s-%s.tar.gz' "$os" "$arch"
            ;;
        *)
            echo "ERROR: unsupported operating system: $os"
            exit 1
            ;;
    esac
}

download_latest_bundle() {
    local asset_name download_url
    asset_name="$(detect_asset_name)"
    download_url="https://github.com/sebastyijan-fi/animasola/releases/latest/download/${asset_name}"
    BUNDLE_ARCHIVE="$TMP_DIR/$asset_name"

    echo "-> Downloading latest release bundle: $asset_name"
    curl -fsSL "$download_url" -o "$BUNDLE_ARCHIVE"
}

if [[ $# -ge 1 ]]; then
    BUNDLE_ARCHIVE="$1"
else
    download_latest_bundle
fi

if [[ ! -f "$BUNDLE_ARCHIVE" ]]; then
    echo "ERROR: bundle archive not found: $BUNDLE_ARCHIVE"
    exit 1
fi

echo "-> Validating release bundle..."
tar -tzf "$BUNDLE_ARCHIVE" > "$BUNDLE_LISTING"
tar -tvzf "$BUNDLE_ARCHIVE" > "$BUNDLE_TYPES"

TOP_LEVEL_DIRS="$(awk -F/ 'NF {print $1}' "$BUNDLE_LISTING" | sort -u)"
TOP_LEVEL_COUNT="$(printf '%s\n' "$TOP_LEVEL_DIRS" | sed '/^$/d' | wc -l | tr -d ' ')"
if [[ "$TOP_LEVEL_COUNT" != "1" ]]; then
    echo "ERROR: release bundle must contain exactly one top-level directory"
    exit 1
fi

BUNDLE_ROOT="$(printf '%s\n' "$TOP_LEVEL_DIRS" | sed -n '1p')"
if [[ -z "$BUNDLE_ROOT" || "$BUNDLE_ROOT" == "." || "$BUNDLE_ROOT" == ".." ]]; then
    echo "ERROR: invalid bundle root directory"
    exit 1
fi

while IFS= read -r entry; do
    [[ -z "$entry" ]] && continue
    if [[ "$entry" == /* || "$entry" == *"../"* || "$entry" == "../"* || "$entry" == *"/.." ]]; then
        echo "ERROR: refusing unsafe bundle path: $entry"
        exit 1
    fi
done < "$BUNDLE_LISTING"

if ! grep -Eq "^${BUNDLE_ROOT}/animasola$" "$BUNDLE_LISTING"; then
    echo "ERROR: bundle is missing animasola binary"
    exit 1
fi
if ! grep -Eq "^${BUNDLE_ROOT}/install.sh$" "$BUNDLE_LISTING"; then
    echo "ERROR: bundle is missing install.sh"
    exit 1
fi
if ! grep -Eq "^${BUNDLE_ROOT}/README.md$" "$BUNDLE_LISTING"; then
    echo "ERROR: bundle is missing README.md"
    exit 1
fi
if ! grep -Eq "^${BUNDLE_ROOT}/bundle-manifest.txt$" "$BUNDLE_LISTING"; then
    echo "ERROR: bundle is missing bundle-manifest.txt"
    exit 1
fi
if ! grep -Eq "^${BUNDLE_ROOT}/bundle-manifest.txt.sig$" "$BUNDLE_LISTING"; then
    echo "ERROR: bundle is missing bundle-manifest.txt.sig"
    exit 1
fi
if ! grep -Eq "^${BUNDLE_ROOT}/tor/tor$" "$BUNDLE_LISTING"; then
    echo "ERROR: bundle is missing bundled tor runtime"
    exit 1
fi

if awk 'substr($1,1,1) !~ /[-d]/ { exit 1 }' "$BUNDLE_TYPES"; then
    :
else
    echo "ERROR: bundle contains unsupported entry types (only regular files and directories are allowed)"
    exit 1
fi

echo "-> Extracting release bundle..."
tar -xzf "$BUNDLE_ARCHIVE" -C "$TMP_DIR" --no-same-owner --no-same-permissions

BUNDLE_DIR="$TMP_DIR/$BUNDLE_ROOT"
if [[ -z "$BUNDLE_DIR" ]]; then
    echo "ERROR: could not find extracted bundle directory"
    exit 1
fi

if [[ ! -f "$BUNDLE_DIR/animasola" || ! -f "$BUNDLE_DIR/install.sh" || ! -f "$BUNDLE_DIR/README.md" || ! -f "$BUNDLE_DIR/tor/tor" ]]; then
    echo "ERROR: extracted bundle contents did not match expected files"
    exit 1
fi
if [[ ! -f "$BUNDLE_DIR/bundle-manifest.txt" || ! -f "$BUNDLE_DIR/bundle-manifest.txt.sig" ]]; then
    echo "ERROR: extracted bundle is missing signed manifest files"
    exit 1
fi

echo "-> Verifying signed bundle contents..."
"$BUNDLE_DIR/animasola" verify-bundle "$BUNDLE_DIR"

echo "-> Installing bundle into $INSTALL_ROOT"
mkdir -p "$INSTALL_PARENT" "$BIN_PARENT"

rm -rf "$INSTALL_ROOT"
mkdir -p "$INSTALL_ROOT"
cp -R "$BUNDLE_DIR"/. "$INSTALL_ROOT"/
chmod +x "$INSTALL_ROOT/animasola"
if [[ -f "$INSTALL_ROOT/tor/tor" ]]; then
    chmod +x "$INSTALL_ROOT/tor/tor"
fi

echo "-> Installing launcher symlink into $BIN_TARGET"
mkdir -p "$BIN_PARENT"
ln -sf "$INSTALL_ROOT/animasola" "$BIN_TARGET"

echo "======================================"
echo "Installation complete."
echo
echo "Run:"
echo "  animasola"
if [[ ":$PATH:" != *":$BIN_PARENT:"* ]]; then
    echo
    echo "Your shell PATH does not include $BIN_PARENT yet."
    echo "Add this line to your shell config, then open a new terminal:"
    echo "  export PATH=\"$BIN_PARENT:\$PATH\""
fi
echo
echo "Verify:"
echo "  animasola doctor"
