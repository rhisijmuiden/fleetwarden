#!/bin/sh
# Stops the gateway before removal. Keys, pairing and configuration are kept
# until the package is purged.
set -eu
if [ -d /run/systemd/system ] && [ "${1:-}" = "remove" ]; then
    systemctl disable --now fleetwarden-gateway-update.path >/dev/null 2>&1 || true
    systemctl disable --now fleetwarden-gateway.service >/dev/null 2>&1 || true
fi
exit 0
