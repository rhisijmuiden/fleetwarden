#!/usr/bin/env bash
# FleetWarden server installer for Debian 13 (trixie): a virtual machine,
# a physical server or a Proxmox container (see docs/install.md).
#
#   curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install.sh | sudo bash
#
# Options:
#   --channel production|test   Release channel to install from (default: production)
#   --deb PATH                  Install a local .deb instead of downloading a release
#   --listen ADDR               Address to listen on, e.g. 0.0.0.0:8080 when the
#                               reverse proxy runs on another machine (default 127.0.0.1:8080)
#   --trusted-proxies LIST      Reverse proxy IPs/CIDRs, comma-separated (added to localhost)
#   --yes                       Do not ask for confirmation
set -euo pipefail

REPO="rhisijmuiden/fleetwarden"
CHANNEL="production"
DEB=""
LISTEN=""
TRUSTED=""
ASSUME_YES=0

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --channel) CHANNEL="${2:-}"; shift 2 ;;
        --deb)     DEB="${2:-}"; shift 2 ;;
        --listen)  LISTEN="${2:-}"; shift 2 ;;
        --trusted-proxies) TRUSTED="${2:-}"; shift 2 ;;
        --yes|-y)  ASSUME_YES=1; shift ;;
        -h|--help) sed -n '2,16p' "$0"; exit 0 ;;
        *) die "Unknown option: $1" ;;
    esac
done
[[ "$CHANNEL" == "production" || "$CHANNEL" == "test" ]] || die "--channel must be production or test"
[[ -z "$LISTEN" || "$LISTEN" =~ ^[^[:space:]|]+:[0-9]+$ ]] || die "--listen must look like 0.0.0.0:8080"
[[ -z "$TRUSTED" || "$TRUSTED" =~ ^[0-9A-Fa-f.:/,]+$ ]] || die "--trusted-proxies must be IPs or CIDRs separated by commas"

[[ $EUID -eq 0 ]] || die "Run this installer as root (sudo)."
[[ -r /etc/os-release ]] || die "Cannot detect the operating system."
# shellcheck disable=SC1091
. /etc/os-release
[[ "${ID:-}" == "debian" && "${VERSION_ID:-}" == "13" ]] || die "FleetWarden requires Debian 13 (trixie); found ${PRETTY_NAME:-unknown}."
[[ "$(dpkg --print-architecture)" == "amd64" ]] || die "Only amd64 servers are supported."

# Works the same on a VM, a physical machine and a container; say which one
# this is and warn when it is smaller than recommended.
virt=$(systemd-detect-virt 2>/dev/null || true)
case "$virt" in
    none|"") machine="physical machine" ;;
    lxc|lxc-libvirt|openvz|docker|podman|systemd-nspawn) machine="container ($virt)" ;;
    *) machine="virtual machine ($virt)" ;;
esac
mem_mb=$(awk '/^MemTotal:/ {print int($2 / 1024)}' /proc/meminfo)
disk_gb=$(df -Pk / | awk 'NR == 2 {print int($4 / 1048576)}')
log "Debian 13 on a ${machine}: ${mem_mb} MB memory, ${disk_gb} GB free disk"
[[ "$mem_mb" -ge 1800 ]] || warn "At least 2 GB memory is recommended (4 GB for 1,000 devices)."
[[ "$disk_gb" -ge 10 ]] || warn "At least 10 GB free disk is recommended (software uploads and backups need more)."

if [[ $ASSUME_YES -eq 0 && -t 0 ]]; then
    read -r -p "Install the FleetWarden server (${CHANNEL} channel) on this machine? [y/N] " answer
    [[ "$answer" =~ ^[Yy]$ ]] || die "Aborted."
fi

export DEBIAN_FRONTEND=noninteractive
log "Installing prerequisites"
apt-get update -qq
apt-get install -y -qq curl ca-certificates jq >/dev/null

if [[ -z "$DEB" ]]; then
    log "Looking up the latest ${CHANNEL} release"
    api="https://api.github.com/repos/${REPO}/releases"
    if [[ "$CHANNEL" == "production" ]]; then
        filter='[.[] | select(.prerelease == false and .draft == false)'
    else
        filter='[.[] | select(.prerelease == true and .draft == false)'
    fi
    # Only releases that ship a server package (not, for example, the LXC template release).
    filter="$filter | select(any(.assets[]?; .name | test(\"^fleetwarden-server_.*_amd64\\\\.deb$\")))]"
    # GitHub does not list releases newest first (v0.1.0-test.8 sorts above
    # v0.1.0-test.30), so pick the most recently published one ourselves.
    filter="$filter | sort_by(.published_at) | last"
    url=$(curl -fsSL "$api?per_page=100" | jq -r "$filter | .assets[]? | select(.name | test(\"^fleetwarden-server_.*_amd64\\\\.deb$\")) | .browser_download_url" | head -n1)
    [[ -n "$url" && "$url" != "null" ]] || die "No ${CHANNEL} release with a server package found."
    DEB="$(mktemp -d)/$(basename "$url")"
    log "Downloading $(basename "$url")"
    curl -fsSL -o "$DEB" "$url"
fi
[[ -f "$DEB" ]] || die "Package not found: $DEB"

log "Installing FleetWarden server and PostgreSQL"
apt-get install -y -qq "$(realpath "$DEB")" >/dev/null

ENV_FILE=/etc/fleetwarden/server.env
restart=0
if [[ -n "$LISTEN" ]]; then
    sed -i "s|^FW_LISTEN=.*|FW_LISTEN=$LISTEN|" "$ENV_FILE"
    restart=1
fi
if [[ -n "$TRUSTED" ]]; then
    sed -i "s|^FW_TRUSTED_PROXIES=.*|FW_TRUSTED_PROXIES=127.0.0.1/32,::1/128,$TRUSTED|" "$ENV_FILE"
    restart=1
fi
if [[ $restart -eq 1 && -d /run/systemd/system ]]; then
    systemctl restart fleetwarden-server.service
fi

if [[ -d /run/systemd/system ]]; then
    for _ in $(seq 1 20); do
        curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break
        sleep 1
    done
    curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 || warn "The server did not answer yet; check: journalctl -u fleetwarden-server"
fi

version=$(/usr/bin/fleetwarden-server -version)
listen=$(sed -n 's/^FW_LISTEN=//p' "$ENV_FILE")
port=${listen##*:}
if [[ "$listen" == 127.0.0.1:* || "$listen" == "[::1]:"* || "$listen" == localhost:* ]]; then
    reach="  The server listens on $listen (this machine only)."
else
    ip=$(ip -4 -o addr show scope global 2>/dev/null | awk '{sub(/\/.*/, "", $4); print $4; exit}' || true)
    reach="  The server listens on $listen: http://${ip:-<this-machine>}:${port}"
fi
PW_FILE=/var/lib/fleetwarden/initial-admin-password
login_info="  Sign in with your existing administrator account."
if [[ -s "$PW_FILE" ]]; then
    login_info="  First sign-in (you must choose a new password right away):
     username: $(sed -n 1p "$PW_FILE")
     password: $(sed -n 2p "$PW_FILE")
  This one-time password is also stored in $PW_FILE
  until you change it."
fi
cat <<MSG

  $version is installed.

$reach

$login_info

  Next steps:
   1. Point your reverse proxy (Zoraxy, Nginx, Caddy, ...) at port ${port}
      and let it handle TLS / Let's Encrypt. Proxy on another host? Run the
      installer again with --listen 0.0.0.0:8080 --trusted-proxies <proxy-ip>,
      or edit FW_LISTEN and FW_TRUSTED_PROXIES in /etc/fleetwarden/server.env.
   2. Open the web interface through your proxy.

  Configuration: /etc/fleetwarden/server.env
  Logs:          journalctl -u fleetwarden-server
  Lost the admin password or authenticator? sudo fleetwarden-server reset-admin

MSG
