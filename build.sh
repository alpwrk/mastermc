#!/usr/bin/env sh
# Builds static Linux binaries for amd64 and arm64 into dist/.
set -e
cd "$(dirname "$0")"
VERSION="${VERSION:-$(date +%Y.%m.%d)}"
mkdir -p dist
for arch in amd64 arm64; do
  echo "→ dist/mastermc-linux-$arch"
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "dist/mastermc-linux-$arch" .
done
