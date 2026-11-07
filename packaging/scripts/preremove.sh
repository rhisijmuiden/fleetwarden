#!/bin/sh
# Stops the service before the package is removed. Data and configuration are kept.
set -eu
if [ -d /run/systemd/system ]; then
    systemctl stop fleetwarden-server.service >/dev/null 2>&1 || true
    if [ "${1:-}" = "remove" ]; then
        systemctl disable --now fleetwarden-update.path >/dev/null 2>&1 || true
        systemctl disable fleetwarden-server.service >/dev/null 2>&1 || true
    fi
fi
exit 0
