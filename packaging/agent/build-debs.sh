#!/usr/bin/env bash
# Builds the agent .deb packages from dist/linux-{amd64,arm64}/fleetwarden-agent
# into dist/agent/ (bundled with the server package and published as release assets).
#   VERSION=0.1.0 packaging/agent/build-debs.sh
set -euo pipefail
: "${VERSION:?set VERSION}"
export VERSION
mkdir -p dist/agent dist/agent-stage
for arch in amd64 arm64; do
    cp "dist/linux-$arch/fleetwarden-agent" dist/agent-stage/fleetwarden-agent
    chmod 0755 dist/agent-stage/fleetwarden-agent
    ARCH="$arch" nfpm package -f packaging/agent/nfpm-agent.yaml -p deb -t "dist/agent/fleetwarden-agent_${VERSION}_${arch}.deb"
done
rm -rf dist/agent-stage
(cd dist/agent && sha256sum fleetwarden-agent_* > SHA256SUMS)
