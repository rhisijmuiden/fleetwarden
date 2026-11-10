#!/usr/bin/env bash
# FleetWarden distribution server installer for a Debian machine in a branch
# office. The web interface (Admin > Distribution servers) shows the exact
# command with a one-time token, for example:
#
#   curl -fsSL https://github.com/rhisijmuiden/fleetwarden/releases/download/v1.0.0/install-distribution.sh \
#     | sudo bash -s -- --server https://fleet.example.com --token fwd_... --version 1.0.0
#
# Run it again (without --token) to update; the configuration, enrollment and
# stored files are kept.
#
# Options:
#   --server URL          The management server, at the address agents use
#   --token TOKEN         One-time token from the web interface (first install)
#   --version VERSION     Install this release (default: the latest of --channel)
#   --channel production|test   Release channel when no version is given (default: production)
#   --listen ADDR         Where agents download (default :8445)
#   --content-dir DIR     Store the files here instead of /var/lib/fleetwarden-distribution/content
#   --min-free-gb N       Disk space to keep free (default 2)
#   --deb PATH            Install a local .deb instead of downloading a release
#   --no-start            Do not start the service (tests without systemd)
#   --yes                 Do not ask for confirmation
set -euo pipefail

REPO="rhisijmuiden/fleetwarden"
ENV_FILE=/etc/fleetwarden-distribution/distribution.env
DATA=/var/lib/fleetwarden-distribution
CHANNEL="production"
VERSION=""
SERVER=""
TOKEN=""
LISTEN=""
CONTENT_DIR=""
MIN_FREE=""
DEB=""
NO_START=0
ASSUME_YES=0

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --server)      SERVER="${2:-}"; shift 2 ;;
        --token)       TOKEN="${2:-}"; shift 2 ;;
        --version)     VERSION="${2:-}"; shift 2 ;;
        --channel)     CHANNEL="${2:-}"; shift 2 ;;
        --listen)      LISTEN="${2:-}"; shift 2 ;;
        --content-dir) CONTENT_DIR="${2:-}"; shift 2 ;;
        --min-free-gb) MIN_FREE="${2:-}"; shift 2 ;;
        --deb)         DEB="${2:-}"; shift 2 ;;
        --no-start)    NO_START=1; shift ;;
        --yes|-y)      ASSUME_YES=1; shift ;;
        -h|--help)     sed -n '2,26p' "$0"; exit 0 ;;
        *) die "Unknown option: $1" ;;
    esac
done
[[ "$CHANNEL" == "production" || "$CHANNEL" == "test" ]] || die "--channel must be production or test"
[[ -z "$VERSION" || "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-test\.[0-9]+)?$ ]] || die "--version must look like 1.2.3 or 1.2.3-test.4"
[[ -z "$SERVER" || "$SERVER" =~ ^https?://[^[:space:]/]+(/)?$ ]] || die "--server must be an address like https://fleet.example.com"
SERVER="${SERVER%/}"
[[ -z "$MIN_FREE" || "$MIN_FREE" =~ ^[0-9]+$ ]] || die "--min-free-gb must be a whole number"
[[ -z "$CONTENT_DIR" || "$CONTENT_DIR" == /* ]] || die "--content-dir must be an absolute path"

[[ $EUID -eq 0 ]] || die "Run this installer as root (sudo)."
[[ -r /etc/os-release ]] || die "Cannot detect the operating system."
# shellcheck disable=SC1091
. /etc/os-release
[[ "${ID:-}" == "debian" || "${ID_LIKE:-}" == *debian* ]] || die "The distribution server needs Debian (13 recommended); found ${PRETTY_NAME:-unknown}."
[[ "${ID:-}" == "debian" && "${VERSION_ID:-}" == "13" ]] || warn "Tested on Debian 13; found ${PRETTY_NAME:-unknown}."
ARCH="$(dpkg --print-architecture)"
[[ "$ARCH" == "amd64" || "$ARCH" == "arm64" ]] || die "Only amd64 and arm64 are supported (found $ARCH)."

ENROLLED=0
[[ -f "$DATA/identity.json" ]] && ENROLLED=1
if [[ $ENROLLED -eq 0 && ( -z "$SERVER" || -z "$TOKEN" ) ]]; then
    die "A first install needs --server and --token; copy the command from Admin > Distribution servers in the web interface."
fi

if [[ $ASSUME_YES -eq 0 ]] && { exec 3</dev/tty; } 2>/dev/null; then
    answer=""
    read -r -u 3 -p "Install the FleetWarden distribution server on this machine? (y/N): " answer || true
    [[ "$answer" =~ ^[Yy] ]] || die "Aborted."
fi

export DEBIAN_FRONTEND=noninteractive
log "Installing prerequisites"
apt-get update -qq
apt-get install -y -qq curl ca-certificates jq >/dev/null

if [[ -z "$DEB" ]]; then
    work=$(mktemp -d)
    trap 'rm -rf "$work"' EXIT
    if [[ -n "$VERSION" ]]; then
        base="https://github.com/${REPO}/releases/download/v${VERSION}"
        name="fleetwarden-distribution_${VERSION}_${ARCH}.deb"
        log "Downloading $name"
        curl -fsSL --retry 3 -o "$work/SHA256SUMS" "$base/SHA256SUMS" || die "Release v$VERSION not found."
        curl -fsSL --retry 3 -o "$work/$name" "$base/$name" || die "Release v$VERSION has no distribution server package for $ARCH."
        (cd "$work" && grep -E "  $name\$" SHA256SUMS | sha256sum -c --status) || die "Checksum mismatch for $name."
        DEB="$work/$name"
    else
        log "Looking up the latest ${CHANNEL} release"
        if [[ "$CHANNEL" == "production" ]]; then
            filter='[.[] | select(.prerelease == false and .draft == false)]'
        else
            filter='[.[] | select(.prerelease == true and .draft == false)]'
        fi
        filter="$filter | sort_by(.published_at) | last"
        url=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases?per_page=100" |
            jq -r "$filter | .assets[]? | select(.name | test(\"^fleetwarden-distribution_.*_${ARCH}\\\\.deb$\")) | .browser_download_url" | head -n1)
        [[ -n "$url" && "$url" != "null" ]] || die "No ${CHANNEL} release with a distribution server package for ${ARCH} found."
        DEB="$work/$(basename "$url")"
        log "Downloading $(basename "$url")"
        curl -fsSL -o "$DEB" "$url"
    fi
fi
[[ -f "$DEB" ]] || die "Package not found: $DEB"

log "Installing the FleetWarden distribution server"
apt-get install -y -qq --allow-downgrades "$(realpath "$DEB")" >/dev/null

set_env() { # key value: replaces or appends KEY=VALUE in the configuration
    local k="$1" v="$2"
    if grep -qE "^#?${k}=" "$ENV_FILE"; then
        sed -i -E "s|^#?${k}=.*|${k}=${v//|/\\|}|" "$ENV_FILE"
    else
        printf '%s=%s\n' "$k" "$v" >>"$ENV_FILE"
    fi
}
[[ -n "$SERVER" ]] && set_env FW_DIST_SERVER "$SERVER"
[[ -n "$LISTEN" ]] && set_env FW_DIST_LISTEN "$LISTEN"
[[ -n "$MIN_FREE" ]] && set_env FW_DIST_MIN_FREE_GB "$MIN_FREE"
if [[ -n "$CONTENT_DIR" ]]; then
    install -d -o fleetwarden-distribution -g fleetwarden-distribution -m 0700 "$CONTENT_DIR"
    set_env FW_DIST_CONTENT_DIR "$CONTENT_DIR"
    # The service may only write its own directories.
    install -d -m 0755 /etc/systemd/system/fleetwarden-distribution.service.d
    printf '[Service]\nReadWritePaths=%s\n' "$CONTENT_DIR" >/etc/systemd/system/fleetwarden-distribution.service.d/content-dir.conf
    [[ -d /run/systemd/system ]] && systemctl daemon-reload
fi

if [[ -n "$TOKEN" ]]; then
    log "Enrolling with $SERVER"
    runuser -u fleetwarden-distribution -- /usr/bin/fleetwarden-distribution enroll --server "$SERVER" --token "$TOKEN" ||
        die "Enrolling failed. Check that this machine reaches $SERVER and that the token is still valid (24 hours)."
fi

if [[ -d /run/systemd/system && $NO_START -eq 0 ]]; then
    systemctl enable fleetwarden-distribution.service >/dev/null 2>&1 || true
    systemctl restart fleetwarden-distribution.service
    sleep 2
    systemctl is-active --quiet fleetwarden-distribution.service || warn "The distribution server is not running; check: journalctl -u fleetwarden-distribution"
fi

version=$(/usr/bin/fleetwarden-distribution -version)
echo
log "$version installed."
runuser -u fleetwarden-distribution -- /usr/bin/fleetwarden-distribution status || true
port=$(sed -n -E 's/^FW_DIST_LISTEN=.*:([0-9]+)$/\1/p' "$ENV_FILE")
cat <<EOF

Next steps
  1. Firewall: allow the agents in this office to reach this machine on TCP ${port:-8445}.
     It only connects out to the management server (like an agent), so no
     rule towards it is needed.
  2. In the web interface, under Admin > Distribution servers, check the address
     agents use and the networks this server serves. Files arrive within minutes.

Configuration: $ENV_FILE    Logs: journalctl -u fleetwarden-distribution
EOF
