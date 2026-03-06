#!/usr/bin/env bash

set -e

echo "======================================"
echo "    Animasola Local Installer         "
echo "======================================"

# Determine target OS and Arch
OS=$(uname | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case $ARCH in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "-> Detected System: $OS ($ARCH)"

# Since we don't have GitHub releases built yet, we will compile it locally
# Requires the Go toolchain to be installed.
if ! command -v go &> /dev/null; then
    echo "ERROR: 'go' is not installed. Please install Go to build Animasola."
    exit 1
fi

echo "-> Building Animasola binary from source..."
go build -o /tmp/animasola ./cmd/animasola/main.go

echo "-> Installing binary to /usr/local/bin/animasola"
echo "-> Subject to sudo requirements..."
sudo mv /tmp/animasola /usr/local/bin/animasola
sudo chmod +x /usr/local/bin/animasola

echo "======================================"
echo "        Installation Complete!        "
echo ""
echo " You can now launch the application "
echo " from any terminal by typing:      "
echo ""
echo "             animasola                "
echo ""
echo "======================================"
