#!/usr/bin/env bash
# FleetWarden secure gateway installer for a Debian machine in the DMZ.
#
#   curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install-gateway.sh | sudo bash
#
# Run it again to update the gateway; the configuration and pairing are kept.
#
# Options (asked interactively when left out):
#   --channel production|test   Release channel (default: production)
#   --tls proxy|files|acme      How agents reach the gateway:
#                                 proxy  behind your reverse proxy (plain HTTP on --listen)
#                                 files  TLS with your own certificate (--cert, --key)
#                                 acme   a Let's Encrypt certificate (--domain, --email; ports 80 and 443)
#   --listen ADDR               Agent listener (default 127.0.0.1:8080 for proxy, :443 otherwise)
#   --trusted-proxies LIST      Reverse proxy addresses (proxy mode; default 127.0.0.1/32,::1/128)
#   --domain NAME[,NAME]        Domain names for Let's Encrypt
#   --email ADDRESS             Contact address for Let's Encrypt (optional)
#   --cert FILE --key FILE      Certificate and key (files mode)
#   --tunnel-allow LIST         Management server address(es) allowed to open the tunnel
#   --deb PATH                  Install a local .deb instead of downloading a release
#   --keep-config               Do not change an existing configuration (default on updates)
#   --yes                       Do not ask questions; use the options and defaults
set -euo pipefail

REPO="rhisijmuiden/fleetwarden"
ENV_FILE=/etc/fleetwarden-gateway/gateway.env
CHANNEL="production"
DEB=""
TLS=""
LISTEN=""
TRUSTED=""
DOMAIN=""
EMAIL=""
CERT=""
KEY=""
TUNNEL_ALLOW=""
KEEP=""
ASSUME_YES=0

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --channel)         CHANNEL="${2:-}"; shift 2 ;;
        --deb)             DEB="${2:-}"; shift 2 ;;
        --tls)             TLS="${2:-}"; shift 2 ;;
        --listen)          LISTEN="${2:-}"; shift 2 ;;
        --trusted-proxies) TRUSTED="${2:-}"; shift 2 ;;
        --domain)          DOMAIN="${2:-}"; shift 2 ;;
        --email)           EMAIL="${2:-}"; shift 2 ;;
        --cert)            CERT="${2:-}"; shift 2 ;;
        --key)             KEY="${2:-}"; shift 2 ;;
        --tunnel-allow)    TUNNEL_ALLOW="${2:-}"; shift 2 ;;
        --keep-config)     KEEP=1; shift ;;
        --yes|-y)          ASSUME_YES=1; shift ;;
        -h|--help)         sed -n '2,25p' "$0"; exit 0 ;;
        *) die "Unknown option: $1" ;;
    esac
done
[[ "$CHANNEL" == "production" || "$CHANNEL" == "test" ]] || die "--channel must be production or test"
[[ -z "$TLS" || "$TLS" == "proxy" || "$TLS" == "files" || "$TLS" == "acme" ]] || die "--tls must be proxy, files or acme"

[[ $EUID -eq 0 ]] || die "Run this installer as root (sudo)."
[[ -r /etc/os-release ]] || die "Cannot detect the operating system."
# shellcheck disable=SC1091
. /etc/os-release
[[ "${ID:-}" == "debian" || "${ID_LIKE:-}" == *debian* ]] || die "The gateway needs Debian (13 recommended); found ${PRETTY_NAME:-unknown}."
[[ "${ID:-}" == "debian" && "${VERSION_ID:-}" == "13" ]] || warn "Tested on Debian 13; found ${PRETTY_NAME:-unknown}."
ARCH="$(dpkg --print-architecture)"
[[ "$ARCH" == "amd64" || "$ARCH" == "arm64" ]] || die "Only amd64 and arm64 are supported (found $ARCH)."

# Questions go to the terminal, also when the script itself comes from a pipe.
TTY=""
if [[ $ASSUME_YES -eq 0 ]] && { exec 3</dev/tty; } 2>/dev/null; then
    TTY=1
fi
ask() { # variable prompt default
    local answer
    if [[ -z "$TTY" ]]; then
        printf -v "$1" '%s' "$3"
        return
    fi
    read -r -u 3 -p "$2 [$3]: " answer || true
    printf -v "$1" '%s' "${answer:-$3}"
}

UPDATE=0
[[ -f "$ENV_FILE" ]] && UPDATE=1
if [[ $UPDATE -eq 1 && -z "$TLS" ]]; then
    KEEP=1 # an update without new options keeps the configuration
fi

if [[ -z "$KEEP" ]]; then
    if [[ -z "$TLS" ]]; then
        echo
        echo "How do agents on the internet reach this gateway?"
        echo "  1) Through a reverse proxy that handles HTTPS (Zoraxy, Nginx, Caddy, ...)"
        echo "  2) Directly, with a free Let's Encrypt certificate (ports 80 and 443 open to the internet)"
        echo "  3) Directly, with my own certificate files"
        choice=""
        ask choice "Choice" "1"
        case "$choice" in 2) TLS=acme ;; 3) TLS=files ;; *) TLS=proxy ;; esac
    fi
    case "$TLS" in
        proxy)
            ask LISTEN "Address the reverse proxy forwards to" "${LISTEN:-127.0.0.1:8080}"
            ask TRUSTED "Reverse proxy addresses (IPs or CIDRs)" "${TRUSTED:-127.0.0.1/32,::1/128}"
            ;;
        acme)
            [[ -n "$DOMAIN" ]] || ask DOMAIN "Public domain name of the gateway (e.g. agents.example.com)" ""
            [[ -n "$DOMAIN" ]] || die "A domain name is needed for Let's Encrypt (--domain)."
            [[ -n "$EMAIL" ]] || ask EMAIL "E-mail address for Let's Encrypt notices (optional)" ""
            LISTEN="${LISTEN:-:443}"
            ;;
        files)
            [[ -n "$CERT" ]] || ask CERT "Certificate file (PEM, full chain)" ""
            [[ -n "$KEY" ]] || ask KEY "Private key file (PEM)" ""
            [[ -r "$CERT" && -r "$KEY" ]] || die "Certificate or key not readable: $CERT $KEY"
            LISTEN="${LISTEN:-:443}"
            ;;
    esac
    [[ -n "$TUNNEL_ALLOW" ]] || ask TUNNEL_ALLOW "IP address of your management server (allowed to open the tunnel; empty allows any)" ""
fi

if [[ -n "$TTY" ]]; then
    go=""
    ask go "Install the FleetWarden gateway (${CHANNEL} channel) on this machine? (y/N)" "N"
    [[ "$go" =~ ^[Yy] ]] || die "Aborted."
fi

export DEBIAN_FRONTEND=noninteractive
log "Installing prerequisites"
apt-get update -qq
apt-get install -y -qq curl ca-certificates jq >/dev/null

if [[ -z "$DEB" ]]; then
    log "Looking up the latest ${CHANNEL} release"
    api="https://api.github.com/repos/${REPO}/releases"
    if [[ "$CHANNEL" == "production" ]]; then
        filter='[.[] | select(.prerelease == false and .draft == false)]'
    else
        filter='[.[] | select(.prerelease == true and .draft == false)]'
    fi
    filter="$filter | sort_by(.published_at) | last"
    url=$(curl -fsSL "$api?per_page=100" | jq -r "$filter | .assets[]? | select(.name | test(\"^fleetwarden-gateway_.*_${ARCH}\\\\.deb$\")) | .browser_download_url" | head -n1)
    [[ -n "$url" && "$url" != "null" ]] || die "No ${CHANNEL} release with a gateway package for ${ARCH} found."
    DEB="$(mktemp -d)/$(basename "$url")"
    log "Downloading $(basename "$url")"
    curl -fsSL -o "$DEB" "$url"
fi
[[ -f "$DEB" ]] || die "Package not found: $DEB"

log "Installing the FleetWarden gateway"
apt-get install -y -qq --allow-downgrades "$(realpath "$DEB")" >/dev/null

set_env() { # key value: replaces or appends KEY=VALUE in the configuration
    local k="$1" v="$2"
    if grep -qE "^#?${k}=" "$ENV_FILE"; then
        sed -i -E "s|^#?${k}=.*|${k}=${v//|/\\|}|" "$ENV_FILE"
    else
        printf '%s=%s\n' "$k" "$v" >>"$ENV_FILE"
    fi
}

if [[ -z "$KEEP" ]]; then
    log "Writing $ENV_FILE"
    set_env FW_GW_TLS "$TLS"
    set_env FW_GW_LISTEN "$LISTEN"
    [[ -n "$TRUSTED" ]] && set_env FW_GW_TRUSTED_PROXIES "$TRUSTED"
    if [[ "$TLS" == "acme" ]]; then
        set_env FW_GW_ACME_DOMAINS "$DOMAIN"
        [[ -n "$EMAIL" ]] && set_env FW_GW_ACME_EMAIL "$EMAIL"
    fi
    if [[ "$TLS" == "files" ]]; then
        install -m 0640 -o root -g fleetwarden-gateway "$CERT" /etc/fleetwarden-gateway/agents.crt
        install -m 0640 -o root -g fleetwarden-gateway "$KEY" /etc/fleetwarden-gateway/agents.key
        set_env FW_GW_TLS_CERT /etc/fleetwarden-gateway/agents.crt
        set_env FW_GW_TLS_KEY /etc/fleetwarden-gateway/agents.key
    fi
    set_env FW_GW_TUNNEL_ALLOW "$TUNNEL_ALLOW"
    if [[ -d /run/systemd/system ]]; then
        systemctl restart fleetwarden-gateway.service
    fi
fi

if [[ -d /run/systemd/system ]]; then
    sleep 2
    systemctl is-active --quiet fleetwarden-gateway.service || warn "The gateway is not running; check: journalctl -u fleetwarden-gateway"
fi

version=$(/usr/bin/fleetwarden-gateway -version)
echo
log "$version installed."
runuser -u fleetwarden-gateway -- /usr/bin/fleetwarden-gateway pairing-code || true
cat <<EOF

Next steps
  1. Firewall: allow the internet to reach this gateway on port 443 (or your
     reverse proxy), and your management server to reach it on TCP 8444.
     No rule from the DMZ into your network is needed.
  2. In the FleetWarden web interface open Admin > Secure gateway > Add gateway
     and enter this machine's address, the public URL for agents and the
     pairing code above.

Configuration: $ENV_FILE    Logs: journalctl -u fleetwarden-gateway
EOF
