#!/usr/bin/env bash
# Builds the FleetWarden Proxmox LXC template: a Debian 13 root filesystem with
# PostgreSQL and every server dependency installed, the server package bundled
# for an offline install at first boot, packed as .tar.zst for Proxmox
# (Datacenter > storage > CT Templates).
#
# Run as root on Debian 13 (CI uses a privileged debian:trixie container):
#   packaging/lxc/build-template.sh dist/fleetwarden-server_X_amd64.deb dist/lxc/fleetwarden-debian-13-lxc_amd64.tar.zst
#
# Environment:
#   MIRROR       Debian mirror (default http://deb.debian.org/debian)
#   APT_OPTIONS  Extra mmdebstrap --aptopt values, separated by newlines
#   ZSTD_LEVEL   zstd compression level (default 19)
set -euo pipefail

deb=$(realpath "${1:?usage: build-template.sh server.deb out.tar.zst}")
out="${2:?usage: build-template.sh server.deb out.tar.zst}"
here=$(cd "$(dirname "$0")" && pwd)
MIRROR="${MIRROR:-http://deb.debian.org/debian}"
SECURITY_MIRROR="${SECURITY_MIRROR:-${MIRROR%/debian}/debian-security}"

[[ $EUID -eq 0 ]] || { echo "build-template.sh must run as root" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive
if ! command -v mmdebstrap >/dev/null || ! command -v zstd >/dev/null; then
    apt-get update -qq
    apt-get install -y -qq mmdebstrap zstd >/dev/null
fi

# Everything the server package depends on or recommends, without versions,
# so first boot installs it with dpkg and no network.
deps=$(dpkg-deb -f "$deb" Depends Recommends | sed -E 's/^[A-Za-z]+: //; s/\([^)]*\)//g' | tr ',\n' '  ' | xargs -n1 | sed 's/|.*//' | sort -u | paste -sd,)
base="systemd,systemd-sysv,dbus,ifupdown,isc-dhcp-client,iproute2,iputils-ping,openssh-server,ca-certificates,curl,jq,less,nano,procps,tzdata,ssl-cert,zstd,cron,logrotate"
echo "Template packages: $base,$deps"

aptopts=()
while IFS= read -r opt; do
    [[ -n "$opt" ]] && aptopts+=(--aptopt="$opt")
done <<<"${APT_OPTIONS:-}"

root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT

# The hooks run in a shell inside mmdebstrap, which sets $1 to the root.
# shellcheck disable=SC2016
mmdebstrap --mode=root --variant=minbase --format=directory \
    --include="$base,$deps" \
    "${aptopts[@]}" \
    --setup-hook='mkdir -p "$1/etc/postgresql-common"; printf "# fleetwarden-template\ncreate_main_cluster = false\n" > "$1/etc/postgresql-common/createcluster.conf"' \
    --customize-hook="install -d \"\$1/usr/lib/fleetwarden-template\" && cp '$deb' \"\$1/usr/lib/fleetwarden-template/\"" \
    --customize-hook="install -m 0755 '$here/fleetwarden-firstboot' \"\$1/usr/lib/fleetwarden-template/fleetwarden-firstboot\"" \
    --customize-hook="install -m 0755 '$here/fleetwarden-issue' \"\$1/usr/lib/fleetwarden-template/fleetwarden-issue\"" \
    --customize-hook="install -m 0644 '$here/fleetwarden-firstboot.service' '$here/fleetwarden-issue.service' '$here/fleetwarden-issue.path' \"\$1/etc/systemd/system/\"" \
    --customize-hook="install -m 0644 '$here/issue' \"\$1/etc/issue\"; : > \"\$1/etc/motd\"" \
    --customize-hook='chroot "$1" systemctl enable fleetwarden-firstboot.service' \
    --customize-hook="printf 'Types: deb\nURIs: http://deb.debian.org/debian\nSuites: trixie trixie-updates\nComponents: main\nSigned-By: /usr/share/keyrings/debian-archive-keyring.gpg\n\nTypes: deb\nURIs: http://security.debian.org/debian-security\nSuites: trixie-security\nComponents: main\nSigned-By: /usr/share/keyrings/debian-archive-keyring.gpg\n' > \"\$1/etc/apt/sources.list.d/debian.sources\"; rm -f \"\$1/etc/apt/sources.list\"" \
    --customize-hook='rm -f "$1"/etc/ssh/ssh_host_* "$1/var/lib/dbus/machine-id"; : > "$1/etc/machine-id"; rm -rf "$1"/var/lib/apt/lists/* "$1"/var/cache/apt/*.bin "$1"/var/log/*.log' \
    trixie "$root" "$MIRROR" "deb $SECURITY_MIRROR trixie-security main"

# The server package must not be configured in the template.
if chroot "$root" dpkg-query -W fleetwarden-server >/dev/null 2>&1; then
    echo "fleetwarden-server is installed in the template; it must only be bundled" >&2
    exit 1
fi
[[ -z "$(chroot "$root" pg_lsclusters -h 2>/dev/null)" ]] || { echo "the template contains a PostgreSQL cluster" >&2; exit 1; }

mkdir -p "$(dirname "$out")"
# Proxmox creates /dev itself; leave device nodes out of the archive.
tar --numeric-owner --xattrs --acls -C "$root" --exclude='./dev/*' -cf - . | zstd -q "-${ZSTD_LEVEL:-19}" -T0 -o "$out" -f
echo "Built $out ($(du -h "$out" | cut -f1))"
