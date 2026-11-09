#!/bin/sh
# On purge the gateway key and pairing are removed too.
set -eu
if [ "${1:-}" = "purge" ]; then
    rm -rf /etc/fleetwarden-gateway /var/lib/fleetwarden-gateway /var/lib/fleetwarden-gateway-update /var/cache/fleetwarden-gateway
fi
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
