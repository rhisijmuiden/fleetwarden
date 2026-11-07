#!/usr/bin/env bash
# FleetWarden server installer for Debian 13 (trixie).
#
#   curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install.sh | sudo bash
#
# Options:
#   --channel production|test   Release channel to install from (default: production)
#   --deb PATH                  Install a local .deb instead of downloading a release
#   --yes                       Do not ask for confirmation
set -euo pipefail

REPO="rhisijmuiden/fleetwarden"
CHANNEL="production"
DEB=""
ASSUME_YES=0

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --channel) CHANNEL="${2:-}"; shift 2 ;;
        --deb)     DEB="${2:-}"; shift 2 ;;
        --yes|-y)  ASSUME_YES=1; shift ;;
        -h|--help) sed -n '2,11p' "$0"; exit 0 ;;
        *) die "Unknown option: $1" ;;
    esac
done
[[ "$CHANNEL" == "production" || "$CHANNEL" == "test" ]] || die "--channel must be production or test"

[[ $EUID -eq 0 ]] || die "Run this installer as root (sudo)."
[[ -r /etc/os-release ]] || die "Cannot detect the operating system."
# shellcheck disable=SC1091
. /etc/os-release
[[ "${ID:-}" == "debian" && "${VERSION_ID:-}" == "13" ]] || die "FleetWarden requires Debian 13 (trixie); found ${PRETTY_NAME:-unknown}."
[[ "$(dpkg --print-architecture)" == "amd64" ]] || die "Only amd64 servers are supported."

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
        filter='[.[] | select(.prerelease == false and .draft == false)][0]'
    else
        filter='[.[] | select(.prerelease == true and .draft == false)][0]'
    fi
    url=$(curl -fsSL "$api?per_page=30" | jq -r "$filter | .assets[]? | select(.name | test(\"^fleetwarden-server_.*_amd64\\\\.deb$\")) | .browser_download_url" | head -n1)
    [[ -n "$url" && "$url" != "null" ]] || die "No ${CHANNEL} release with a server package found."
    DEB="$(mktemp -d)/$(basename "$url")"
    log "Downloading $(basename "$url")"
    curl -fsSL -o "$DEB" "$url"
fi
[[ -f "$DEB" ]] || die "Package not found: $DEB"

log "Installing FleetWarden server and PostgreSQL"
apt-get install -y -qq "$(realpath "$DEB")" >/dev/null

if [[ -d /run/systemd/system ]]; then
    for _ in $(seq 1 20); do
        curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break
        sleep 1
    done
    curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 || warn "The server did not answer yet; check: journalctl -u fleetwarden-server"
fi

version=$(/usr/bin/fleetwarden-server -version)
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

$login_info

  Next steps:
   1. Point your reverse proxy (Zoraxy, Nginx, Caddy, ...) at http://127.0.0.1:8080
      and let it handle TLS / Let's Encrypt. Proxy on another host? Edit
      FW_LISTEN and FW_TRUSTED_PROXIES in /etc/fleetwarden/server.env.
   2. Open the web interface through your proxy.

  Configuration: /etc/fleetwarden/server.env
  Logs:          journalctl -u fleetwarden-server
  Lost the admin password or authenticator? sudo fleetwarden-server reset-admin

MSG
