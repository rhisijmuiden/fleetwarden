#!/bin/sh
# Post-install script for the fleetwarden-agent package. On first install it
# configures the agent from (in order): the enrollment file baked into a
# package from the Add client wizard, FW_SERVER/FW_TOKEN/FW_GROUP from the
# environment, or the debconf answers. Then it (re)starts the service.
set -e

CONF=/etc/fleetwarden-agent/agent.json
ENROLL=/usr/share/fleetwarden-agent/enroll.json

if [ -e /usr/share/debconf/confmodule ]; then
    # shellcheck disable=SC1091
    . /usr/share/debconf/confmodule
fi

[ "${1:-}" = "configure" ] || exit 0

install -d -m 0700 /etc/fleetwarden-agent

if [ ! -s "$CONF" ]; then
    if [ -s "$ENROLL" ]; then
        fleetwarden-agent configure --from-file "$ENROLL" || echo "fleetwarden-agent: could not use $ENROLL" >&2
    elif [ -n "${FW_SERVER:-}" ] && [ -n "${FW_TOKEN:-}" ]; then
        fleetwarden-agent configure --server "$FW_SERVER" --token "$FW_TOKEN" --group "${FW_GROUP:-}" || true
    elif command -v db_get >/dev/null 2>&1; then
        server="" token=""
        db_get fleetwarden-agent/server && server="${RET:-}"
        db_get fleetwarden-agent/token && token="${RET:-}"
        # Do not keep the token in the debconf database.
        db_set fleetwarden-agent/token "" || true
        if [ -n "$server" ] && [ -n "$token" ]; then
            fleetwarden-agent configure --server "$server" --token "$token" || true
        fi
    fi
fi
# The enrollment file is only needed once.
[ -s "$CONF" ] && [ -f "$ENROLL" ] && : > "$ENROLL"

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl enable fleetwarden-agent.service >/dev/null 2>&1 || true
    if [ -s "$CONF" ]; then
        systemctl restart fleetwarden-agent.service
    else
        echo "fleetwarden-agent: not configured yet. Run:"
        echo "  sudo fleetwarden-agent configure --server https://em.example.com --token TOKEN"
        echo "  sudo systemctl restart fleetwarden-agent"
    fi
fi
if command -v db_stop >/dev/null 2>&1; then
    db_stop
fi
exit 0
