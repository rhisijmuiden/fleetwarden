#!/bin/sh
# Post-install script for the fleetwarden-gateway Debian package. Creates the
# service user and a default configuration on first install; upgrades keep
# the configuration, keys and pairing.
set -eu

CONF_DIR=/etc/fleetwarden-gateway
ENV_FILE=$CONF_DIR/gateway.env
DATA=/var/lib/fleetwarden-gateway

if ! getent passwd fleetwarden-gateway >/dev/null; then
    adduser --system --group --home "$DATA" --no-create-home --quiet fleetwarden-gateway
fi
install -d -o fleetwarden-gateway -g fleetwarden-gateway -m 0700 "$DATA"
install -d -o fleetwarden-gateway -g fleetwarden-gateway -m 0700 "$DATA/update"
install -d -o root -g root -m 0755 /var/lib/fleetwarden-gateway-update /var/cache/fleetwarden-gateway
install -d -o root -g fleetwarden-gateway -m 0750 "$CONF_DIR"

if [ ! -f "$ENV_FILE" ]; then
    umask 027
    cat >"$ENV_FILE" <<CONF
# FleetWarden secure gateway configuration. Restart after changes:
#   systemctl restart fleetwarden-gateway
# The installer (install-gateway.sh) fills this in; see docs/gateway.md.

# How agents reach the gateway:
#   proxy  plain HTTP behind a reverse proxy that terminates TLS (default)
#   files  TLS with your own certificate (FW_GW_TLS_CERT, FW_GW_TLS_KEY)
#   acme   a Let's Encrypt certificate for FW_GW_ACME_DOMAINS (needs ports 80 and 443)
FW_GW_TLS=proxy
FW_GW_LISTEN=127.0.0.1:8080
FW_GW_TRUSTED_PROXIES=127.0.0.1/32,::1/128
#FW_GW_TLS_CERT=/etc/fleetwarden-gateway/agents.crt
#FW_GW_TLS_KEY=/etc/fleetwarden-gateway/agents.key
#FW_GW_ACME_DOMAINS=agents.example.com
#FW_GW_ACME_EMAIL=it@example.com

# The management server opens its tunnel to this port (TLS, self-signed is fine).
FW_GW_TUNNEL_LISTEN=:8444
# Only these addresses may open the tunnel (comma-separated IPs or CIDRs).
# Set this to your management server's address.
FW_GW_TUNNEL_ALLOW=
CONF
    chown root:fleetwarden-gateway "$ENV_FILE"
    chmod 0640 "$ENV_FILE"
fi

# Create the gateway key before the service starts, and show the pairing code
# on a first install (and whenever the gateway is not paired).
if [ ! -f "$DATA/server.pub" ]; then
    runuser -u fleetwarden-gateway -- /usr/bin/fleetwarden-gateway pairing-code 2>/dev/null || true
fi

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl enable fleetwarden-gateway.service >/dev/null 2>&1 || true
    systemctl enable --now fleetwarden-gateway-update.path >/dev/null 2>&1 || true
    systemctl restart fleetwarden-gateway.service || true
fi

exit 0
