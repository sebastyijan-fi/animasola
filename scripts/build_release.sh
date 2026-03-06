#!/usr/bin/env bash

set -e

echo "================================================="
echo "   Building Animasola Releases (Linux & macOS)   "
echo "================================================="

# Create releases directory
rm -rf releases/
mkdir -p releases/

# 1. Linux AMD64
echo "Building Linux amd64..."
GOOS=linux GOARCH=amd64 go build -o releases/animasola-linux-amd64 cmd/animasola/main.go

# 2. Linux ARM64 (Raspberry Pi, etc)
echo "Building Linux arm64..."
GOOS=linux GOARCH=arm64 go build -o releases/animasola-linux-arm64 cmd/animasola/main.go

# 3. macOS Intel
echo "Building macOS amd64..."
GOOS=darwin GOARCH=amd64 go build -o releases/animasola-darwin-amd64 cmd/animasola/main.go

# 4. macOS Apple Silicon (M1/M2/M3)
echo "Building macOS arm64..."
GOOS=darwin GOARCH=arm64 go build -o releases/animasola-darwin-arm64 cmd/animasola/main.go

echo "================================================="
echo "Generating SHA256 checksums for security verification..."
cd releases
sha256sum animasola-* > checksums.txt
cd ..

echo "================================================="
echo "Done! Upload the 4 binaries AND checksums.txt to the GitHub 'Releases' page."
