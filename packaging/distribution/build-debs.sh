#!/usr/bin/env bash
# Builds the distribution server .deb packages from
# dist/linux-{amd64,arm64}/fleetwarden-distribution into dist/distribution/
# (published as release assets).
#   VERSION=0.1.0 packaging/distribution/build-debs.sh
set -euo pipefail
: "${VERSION:?set VERSION}"
export VERSION
mkdir -p dist/distribution dist/distribution-stage
for arch in amd64 arm64; do
    cp "dist/linux-$arch/fleetwarden-distribution" dist/distribution-stage/fleetwarden-distribution
    chmod 0755 dist/distribution-stage/fleetwarden-distribution
    ARCH="$arch" nfpm package -f packaging/distribution/nfpm-distribution.yaml -p deb -t "dist/distribution/fleetwarden-distribution_${VERSION}_${arch}.deb"
done
rm -rf dist/distribution-stage
