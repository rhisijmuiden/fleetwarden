#!/bin/sh
# Post-install script for the fleetwarden-server Debian package.
# Creates the service user, the PostgreSQL database and the configuration file
# on first install. Upgrades keep the existing configuration untouched.
set -eu

CONF_DIR=/etc/fleetwarden
ENV_FILE=$CONF_DIR/server.env
DB_NAME=fleetwarden
DB_USER=fleetwarden

if ! getent passwd fleetwarden >/dev/null; then
    adduser --system --group --home /var/lib/fleetwarden --no-create-home --quiet fleetwarden
fi
install -d -o fleetwarden -g fleetwarden -m 0750 /var/lib/fleetwarden /var/log/fleetwarden
install -d -o root -g fleetwarden -m 0750 "$CONF_DIR"

has_systemd() { [ -d /run/systemd/system ]; }

pg_running() { runuser -u postgres -- psql -qtAc 'SELECT 1' >/dev/null 2>&1; }

ensure_postgres() {
    pg_running && return 0
    if has_systemd; then
        systemctl start postgresql || true
    else
        # Containers and chroots without systemd: start the default cluster directly.
        for cluster in $(pg_lsclusters -h 2>/dev/null | awk '{print $1"/"$2}'); do
            pg_ctlcluster --skip-systemctl-redirect "${cluster%/*}" "${cluster#*/}" start || true
        done
    fi
    i=0
    while ! pg_running; do
        i=$((i + 1))
        [ "$i" -gt 30 ] && { echo "fleetwarden: PostgreSQL did not start" >&2; return 1; }
        sleep 1
    done
}

if [ ! -f "$ENV_FILE" ]; then
    ensure_postgres
    DB_PASS=$(head -c 32 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 32)
    if ! runuser -u postgres -- psql -qtAc "SELECT 1 FROM pg_roles WHERE rolname='$DB_USER'" | grep -q 1; then
        runuser -u postgres -- psql -qc "CREATE ROLE $DB_USER LOGIN PASSWORD '$DB_PASS'"
    else
        runuser -u postgres -- psql -qc "ALTER ROLE $DB_USER PASSWORD '$DB_PASS'"
    fi
    if ! runuser -u postgres -- psql -qtAc "SELECT 1 FROM pg_database WHERE datname='$DB_NAME'" | grep -q 1; then
        runuser -u postgres -- createdb -O "$DB_USER" "$DB_NAME"
    fi
    umask 027
    cat >"$ENV_FILE" <<CONF
# FleetWarden server configuration. Restart the service after changes:
#   systemctl restart fleetwarden-server

# Address the server listens on. FleetWarden is designed to run behind a
# reverse proxy (Zoraxy, Nginx, Caddy, Traefik, ...) that terminates TLS.
# Use 0.0.0.0:8080 when the proxy runs on another machine.
FW_LISTEN=127.0.0.1:8080

# Reverse proxies whose X-Forwarded-For header is trusted (comma-separated IPs or CIDRs).
FW_TRUSTED_PROXIES=127.0.0.1/32,::1/128

FW_DATABASE_URL=postgres://$DB_USER:$DB_PASS@127.0.0.1:5432/$DB_NAME?sslmode=disable
FW_WEB_DIR=/usr/share/fleetwarden/web
CONF
    chown root:fleetwarden "$ENV_FILE"
    chmod 0640 "$ENV_FILE"
fi

if has_systemd; then
    systemctl daemon-reload
    systemctl enable fleetwarden-server.service >/dev/null 2>&1 || true
    systemctl restart fleetwarden-server.service
fi
exit 0
