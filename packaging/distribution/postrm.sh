#!/bin/sh
# On purge the key, enrollment and stored files are removed too.
set -eu
if [ "${1:-}" = "purge" ]; then
    rm -rf /etc/fleetwarden-distribution /var/lib/fleetwarden-distribution /var/lib/fleetwarden-distribution-update /var/cache/fleetwarden-distribution
fi
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
