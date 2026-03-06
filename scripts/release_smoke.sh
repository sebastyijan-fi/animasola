#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
RELEASES_DIR="$ROOT_DIR/releases"

cleanup() {
    rm -rf "$TMP_ROOT"
}
trap cleanup EXIT

os="$(uname | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
    x86_64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) echo "unsupported architecture: $arch" ; exit 1 ;;
esac

artifact="animasola-${os}-${arch}.tar.gz"

echo "==> building release bundles"
"$ROOT_DIR/scripts/build_release.sh"

bundle_path="$RELEASES_DIR/$artifact"
if [[ ! -f "$bundle_path" ]]; then
    echo "missing bundle: $bundle_path"
    exit 1
fi

echo "==> verifying bundled contents"
bundle_listing="$TMP_ROOT/bundle-listing.txt"
tar -tzf "$bundle_path" >"$bundle_listing"
if ! grep -Eq '/animasola$' "$bundle_listing"; then
    echo "bundle is missing animasola binary"
    exit 1
fi
if ! grep -Eq '/install.sh$' "$bundle_listing"; then
    echo "bundle is missing install.sh"
    exit 1
fi
if ! grep -Eq '/tor/tor$' "$bundle_listing"; then
    echo "bundle is missing bundled tor runtime"
    exit 1
fi

install_root="$TMP_ROOT/install-root/animasola"
bin_target="$TMP_ROOT/bin/animasola"
config_root="$TMP_ROOT/config"

echo "==> installing bundle into temp prefix"
ANIMASOLA_INSTALL_ROOT="$install_root" \
ANIMASOLA_BIN_TARGET="$bin_target" \
SUDO_BIN="" \
"$ROOT_DIR/install.sh" "$bundle_path"

echo "==> running packaged doctor"
HOME="$TMP_ROOT/home" \
XDG_CONFIG_HOME="$config_root" \
"$bin_target" doctor >"$TMP_ROOT/doctor.txt"

cat "$TMP_ROOT/doctor.txt"

if ! rg -q '^Tor: ok ' "$TMP_ROOT/doctor.txt"; then
    echo "doctor did not report a working tor runtime"
    exit 1
fi

echo "==> release smoke passed"
