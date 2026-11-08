#!/usr/bin/env bash
# Builds the gateway .deb packages from dist/linux-{amd64,arm64}/fleetwarden-gateway
# into dist/gateway/ (published as release assets).
#   VERSION=0.1.0 packaging/gateway/build-debs.sh
set -euo pipefail
: "${VERSION:?set VERSION}"
export VERSION
mkdir -p dist/gateway dist/gateway-stage
for arch in amd64 arm64; do
    cp "dist/linux-$arch/fleetwarden-gateway" dist/gateway-stage/fleetwarden-gateway
    chmod 0755 dist/gateway-stage/fleetwarden-gateway
    ARCH="$arch" nfpm package -f packaging/gateway/nfpm-gateway.yaml -p deb -t "dist/gateway/fleetwarden-gateway_${VERSION}_${arch}.deb"
done
rm -rf dist/gateway-stage
