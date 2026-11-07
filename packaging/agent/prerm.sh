#!/bin/sh
# Stops the agent before removal. On an upgrade the service keeps running
# until postinst restarts it with the new binary.
set -eu
if [ -d /run/systemd/system ] && [ "${1:-}" = "remove" ]; then
    systemctl disable --now fleetwarden-agent.service >/dev/null 2>&1 || true
fi
exit 0
