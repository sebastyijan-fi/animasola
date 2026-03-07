#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASES_DIR="$ROOT_DIR/releases"
DEFAULT_SIGNING_KEY="$ROOT_DIR/.keys/release-signing-private.b64"

detect_host_target() {
    local os arch
    os="$(uname | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"
    case "$arch" in
        x86_64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *) echo "unsupported architecture: $arch" >&2 ; exit 1 ;;
    esac
    printf '%s/%s' "$os" "$arch"
}

SIGNING_KEY="${ANIMASOLA_RELEASE_SIGNING_KEY:-}"
if [[ -z "$SIGNING_KEY" && -f "$DEFAULT_SIGNING_KEY" ]]; then
    SIGNING_KEY="$DEFAULT_SIGNING_KEY"
fi

if [[ -z "${ANIMASOLA_RELEASE_TARGETS:-}" ]]; then
    ANIMASOLA_RELEASE_TARGETS="$(detect_host_target)"
fi

echo "=========================================================="
echo "   Building Animasola Release Bundles (with bundled Tor)  "
echo "=========================================================="

rm -rf "$RELEASES_DIR"
mkdir -p "$RELEASES_DIR"

resolve_tor_runtime_dir() {
    local os="$1"
    local arch="$2"

    if [[ -n "${ANIMASOLA_TOR_RUNTIME_DIR:-}" && -d "${ANIMASOLA_TOR_RUNTIME_DIR}" ]]; then
        if [[ -f "${ANIMASOLA_TOR_RUNTIME_DIR}/tor" ]]; then
            printf '%s' "${ANIMASOLA_TOR_RUNTIME_DIR}"
            return 0
        fi
        echo "ANIMASOLA_TOR_RUNTIME_DIR is missing tor binary: ${ANIMASOLA_TOR_RUNTIME_DIR}/tor" >&2
        return 1
    fi

    if [[ -n "${ANIMASOLA_TOR_BIN:-}" && -f "${ANIMASOLA_TOR_BIN}" ]]; then
        printf '%s' "$(dirname "${ANIMASOLA_TOR_BIN}")"
        return 0
    fi

    local bundled="$ROOT_DIR/tor/${os}-${arch}"
    if [[ -f "$bundled/tor" ]]; then
        printf '%s' "$bundled"
        return 0
    fi

    return 1
}

should_skip_tor_runtime_entry() {
    local name="$1"
    case "$name" in
        ld-linux*.so*|libc.so*|libpthread.so*|libdl.so*|librt.so*|libm.so*|libresolv.so*|libutil.so*|libanl.so*|libnss_*.so*|libnsl.so*|libcrypt.so*)
            return 0
            ;;
    esac
    return 1
}

copy_tor_runtime() {
    local src_dir="$1"
    local dest_dir="$2"
    local path rel name

    while IFS= read -r -d '' path; do
        rel="${path#$src_dir/}"
        name="$(basename "$path")"
        if should_skip_tor_runtime_entry "$name"; then
            continue
        fi
        if [[ -d "$path" ]]; then
            mkdir -p "$dest_dir/$rel"
            continue
        fi
        mkdir -p "$(dirname "$dest_dir/$rel")"
        cp "$path" "$dest_dir/$rel"
    done < <(find "$src_dir" -mindepth 1 -print0 | LC_ALL=C sort -z)
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

    if ! tor_runtime_dir="$(resolve_tor_runtime_dir "$os" "$arch")"; then
        echo "missing tor runtime for $artifact"
        echo "provide one of:"
        echo "  1. ANIMASOLA_TOR_RUNTIME_DIR=/path/to/runtime-dir"
        echo "  2. ANIMASOLA_TOR_BIN=/path/to/tor (with required shared libs in the same directory)"
        echo "  3. ./tor/${os}-${arch}/tor"
        exit 1
    fi
    copy_tor_runtime "$tor_runtime_dir" "$bundle_dir/tor"
    chmod +x "$bundle_dir/tor/"*
    echo "  bundled tor runtime: $tor_runtime_dir"

    (
        cd "$bundle_dir"
        find . -type f ! -name "bundle-manifest.txt" ! -name "bundle-manifest.txt.sig" \
            | sed 's|^\./||' \
            | LC_ALL=C sort \
            | while IFS= read -r rel; do
                sha256sum "$rel"
            done > bundle-manifest.txt
    )

    (
        cd "$ROOT_DIR"
        go run ./cmd/release-sign \
            -key-file "$SIGNING_KEY" \
            -input "$bundle_dir/bundle-manifest.txt" \
            -output "$bundle_dir/bundle-manifest.txt.sig"
    )

    (
        cd "$RELEASES_DIR"
        tar -czf "${artifact}.tar.gz" "$artifact"
    )
}

if [[ -z "$SIGNING_KEY" ]]; then
    echo "missing release signing key"
    echo "expected one of:"
    echo "  1. ANIMASOLA_RELEASE_SIGNING_KEY=/path/to/private-key"
    echo "  2. $DEFAULT_SIGNING_KEY"
    exit 1
fi

for target in $ANIMASOLA_RELEASE_TARGETS; do
    os="${target%/*}"
    arch="${target#*/}"
    if [[ -z "$os" || -z "$arch" || "$os" == "$arch" ]]; then
        echo "invalid release target: $target"
        echo "expected format: os/arch"
        exit 1
    fi
    build_bundle "$os" "$arch"
done

(
    cd "$RELEASES_DIR"
    sha256sum ./*.tar.gz > checksums.txt
)

echo "==> signing release manifest"
(
    cd "$ROOT_DIR"
    go run ./cmd/release-sign \
        -key-file "$SIGNING_KEY" \
        -input "$RELEASES_DIR/checksums.txt" \
        -output "$RELEASES_DIR/checksums.txt.sig"
)

echo "=========================================================="
echo "Done."
echo "Artifacts:"
echo "  releases/animasola-<os>-<arch>.tar.gz"
echo "  releases/checksums.txt"
echo "  releases/checksums.txt.sig"
echo "Targets:"
for target in $ANIMASOLA_RELEASE_TARGETS; do
    echo "  $target"
done
