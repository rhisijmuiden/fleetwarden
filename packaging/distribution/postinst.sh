#!/bin/sh
# Post-install script for the fleetwarden-distribution Debian package. Creates
# the service user and a default configuration on first install; upgrades keep
# the configuration, key, enrollment and stored files.
set -eu

CONF_DIR=/etc/fleetwarden-distribution
ENV_FILE=$CONF_DIR/distribution.env
DATA=/var/lib/fleetwarden-distribution

if ! getent passwd fleetwarden-distribution >/dev/null; then
    adduser --system --group --home "$DATA" --no-create-home --quiet fleetwarden-distribution
fi
install -d -o fleetwarden-distribution -g fleetwarden-distribution -m 0700 "$DATA"
install -d -o fleetwarden-distribution -g fleetwarden-distribution -m 0700 "$DATA/update"
install -d -o root -g root -m 0755 /var/lib/fleetwarden-distribution-update /var/cache/fleetwarden-distribution
install -d -o root -g fleetwarden-distribution -m 0750 "$CONF_DIR"

if [ ! -f "$ENV_FILE" ]; then
    umask 027
    cat >"$ENV_FILE" <<CONF
# FleetWarden distribution server configuration. Restart after changes:
#   systemctl restart fleetwarden-distribution
# The installer (install-distribution.sh) fills this in; see docs/distribution.md.

# The management server, at the address agents use.
FW_DIST_SERVER=
# Agents download here (HTTPS with a self-signed certificate they pin).
FW_DIST_LISTEN=:8445
# Disk space to keep free, in gigabytes.
FW_DIST_MIN_FREE_GB=2
# Store the files on another disk:
#FW_DIST_CONTENT_DIR=/srv/fleetwarden-distribution
# Your own certificate instead of the generated one (agents pin either):
#FW_DIST_TLS_CERT=/etc/fleetwarden-distribution/agents.crt
#FW_DIST_TLS_KEY=/etc/fleetwarden-distribution/agents.key
# An internal CA for the management server's certificate:
#FW_DIST_CA_FILE=/etc/fleetwarden-distribution/ca.crt
CONF
    chown root:fleetwarden-distribution "$ENV_FILE"
    chmod 0640 "$ENV_FILE"
fi

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl enable fleetwarden-distribution.service >/dev/null 2>&1 || true
    systemctl enable --now fleetwarden-distribution-update.path >/dev/null 2>&1 || true
    # It only runs once enrolled; the installer starts it after enrolling.
    if [ -f "$DATA/identity.json" ]; then
        systemctl restart fleetwarden-distribution.service || true
    fi
fi

exit 0
