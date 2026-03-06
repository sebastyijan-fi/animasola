#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASES_DIR="$ROOT_DIR/releases"

echo "=========================================================="
echo "   Building Animasola Release Bundles (with bundled Tor)  "
echo "=========================================================="

rm -rf "$RELEASES_DIR"
mkdir -p "$RELEASES_DIR"

resolve_tor_binary() {
    local os="$1"
    local arch="$2"

    if [[ -n "${ANIMASOLA_TOR_BIN:-}" && -f "${ANIMASOLA_TOR_BIN}" ]]; then
        printf '%s' "${ANIMASOLA_TOR_BIN}"
        return 0
    fi

    if [[ "$os" == "$(uname | tr '[:upper:]' '[:lower:]')" ]]; then
        if command -v tor >/dev/null 2>&1; then
            command -v tor
            return 0
        fi
    fi

    local bundled="$ROOT_DIR/tor/${os}-${arch}/tor"
    if [[ "$os" == "windows" ]]; then
        bundled="$ROOT_DIR/tor/${os}-${arch}/tor.exe"
    fi
    if [[ -f "$bundled" ]]; then
        printf '%s' "$bundled"
        return 0
    fi

    return 1
}

build_bundle() {
    local os="$1"
    local arch="$2"
    local artifact="animasola-${os}-${arch}"
    local bundle_dir="$RELEASES_DIR/$artifact"

    echo "Building $artifact..."
    mkdir -p "$bundle_dir/tor"
    GOOS="$os" GOARCH="$arch" go build -o "$bundle_dir/animasola" "$ROOT_DIR/cmd/animasola/main.go"
    cp "$ROOT_DIR/install.sh" "$bundle_dir/install.sh"
    cp "$ROOT_DIR/README.md" "$bundle_dir/README.md"
    chmod +x "$bundle_dir/install.sh"

    if tor_bin="$(resolve_tor_binary "$os" "$arch")"; then
        cp "$tor_bin" "$bundle_dir/tor/"
        chmod +x "$bundle_dir/tor/"*
        echo "  bundled tor: $tor_bin"
    else
        echo "  warning: tor binary not found for $artifact"
        echo "  release will require system tor or ANIMASOLA_TOR_BIN"
    fi

    (
        cd "$RELEASES_DIR"
        tar -czf "${artifact}.tar.gz" "$artifact"
    )
}

build_bundle linux amd64
build_bundle linux arm64
build_bundle darwin amd64
build_bundle darwin arm64

(
    cd "$RELEASES_DIR"
    sha256sum ./*.tar.gz > checksums.txt
)

echo "=========================================================="
echo "Done."
echo "Artifacts:"
echo "  releases/animasola-<os>-<arch>.tar.gz"
echo "  releases/checksums.txt"
echo
echo "If you want bundled Tor, provide one of:"
echo "  1. ANIMASOLA_TOR_BIN=/path/to/tor"
echo "  2. ./tor/<os>-<arch>/tor"
