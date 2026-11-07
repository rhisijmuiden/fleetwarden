#!/bin/sh
# On purge the enrollment (device key) is removed too.
set -eu
if [ "${1:-}" = "purge" ]; then
    rm -rf /etc/fleetwarden-agent /var/lib/fleetwarden-agent
    if [ -e /usr/share/debconf/confmodule ]; then
        # shellcheck disable=SC1091
        . /usr/share/debconf/confmodule
        db_purge || true
    fi
fi
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
