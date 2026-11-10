#!/bin/sh
# Stops the distribution server before removal. The key, enrollment, stored
# files and configuration are kept until the package is purged.
set -eu
if [ -d /run/systemd/system ] && [ "${1:-}" = "remove" ]; then
    systemctl disable --now fleetwarden-distribution-update.path >/dev/null 2>&1 || true
    systemctl disable --now fleetwarden-distribution.service >/dev/null 2>&1 || true
fi
exit 0
