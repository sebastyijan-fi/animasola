#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
ORIGINAL_HOME="${HOME:-}"

cleanup() {
    rm -rf "$TMP_ROOT"
}
trap cleanup EXIT

if [[ -n "$ORIGINAL_HOME" ]]; then
    export GOPATH="${GOPATH:-$ORIGINAL_HOME/go}"
    export GOMODCACHE="${GOMODCACHE:-$ORIGINAL_HOME/go/pkg/mod}"
    export GOCACHE="${GOCACHE:-$ORIGINAL_HOME/.cache/go-build}"
fi

if [[ -z "${ANIMASOLA_TOR_BIN:-}" ]]; then
    os="$(uname | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"
    case "$arch" in
        x86_64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
    esac

    bundled_tor="$ROOT_DIR/releases/animasola-${os}-${arch}/tor/tor"
    if [[ -f "$bundled_tor" ]]; then
        export ANIMASOLA_TOR_BIN="$bundled_tor"
    fi
fi

export HOME="$TMP_ROOT/home"
export XDG_CONFIG_HOME="$TMP_ROOT/config"
export XDG_DATA_HOME="$TMP_ROOT/data"
export XDG_CACHE_HOME="$TMP_ROOT/cache"
export ANIMASOLA_DEBUG_NETWORK=0

mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME"

cd "$ROOT_DIR"
ATTACK_LAB_BIN="$TMP_ROOT/attack-lab"
go build -o "$ATTACK_LAB_BIN" ./cmd/attack-lab
"$ATTACK_LAB_BIN" "$@"
