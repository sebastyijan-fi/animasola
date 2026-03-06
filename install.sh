#!/usr/bin/env bash

set -euo pipefail

echo "======================================"
echo "      Animasola Bundle Installer      "
echo "======================================"

if [[ $# -lt 1 ]]; then
    echo "Usage: ./install.sh /path/to/animasola-<os>-<arch>.tar.gz"
    exit 1
fi

BUNDLE_ARCHIVE="$1"
INSTALL_ROOT="${ANIMASOLA_INSTALL_ROOT:-/usr/local/lib/animasola}"
BIN_TARGET="${ANIMASOLA_BIN_TARGET:-/usr/local/bin/animasola}"
TMP_DIR="$(mktemp -d)"
SUDO="${SUDO_BIN:-sudo}"
INSTALL_PARENT="$(dirname "$INSTALL_ROOT")"
BIN_PARENT="$(dirname "$BIN_TARGET")"

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

if [[ ! -f "$BUNDLE_ARCHIVE" ]]; then
    echo "ERROR: bundle archive not found: $BUNDLE_ARCHIVE"
    exit 1
fi

echo "-> Extracting release bundle..."
tar -xzf "$BUNDLE_ARCHIVE" -C "$TMP_DIR"

BUNDLE_DIR="$(find "$TMP_DIR" -maxdepth 1 -mindepth 1 -type d | head -n 1)"
if [[ -z "$BUNDLE_DIR" ]]; then
    echo "ERROR: could not find extracted bundle directory"
    exit 1
fi

echo "-> Installing bundle into $INSTALL_ROOT"
mkdir -p "$INSTALL_PARENT" "$BIN_PARENT"
if [[ -w "$INSTALL_PARENT" && -w "$BIN_PARENT" ]]; then
    SUDO=""
fi

$SUDO rm -rf "$INSTALL_ROOT"
$SUDO mkdir -p "$INSTALL_ROOT"
$SUDO cp -R "$BUNDLE_DIR"/. "$INSTALL_ROOT"/
$SUDO chmod +x "$INSTALL_ROOT/animasola"
if [[ -f "$INSTALL_ROOT/tor/tor" ]]; then
    $SUDO chmod +x "$INSTALL_ROOT/tor/tor"
fi

echo "-> Installing launcher symlink into $BIN_TARGET"
$SUDO mkdir -p "$BIN_PARENT"
$SUDO ln -sf "$INSTALL_ROOT/animasola" "$BIN_TARGET"

echo "======================================"
echo "Installation complete."
echo
echo "Run:"
echo "  animasola"
echo
echo "Verify:"
echo "  animasola doctor"
