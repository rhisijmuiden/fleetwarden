#!/usr/bin/env bash
# Boots the LXC template the way Proxmox does (systemd as PID 1, no network
# configured beyond what the host gives) and checks that first boot sets up a
# working FleetWarden server and shows only the URL and first sign-in on the
# console screen.
#   sudo tests/lxc-template-test.sh dist/lxc/fleetwarden-debian-13-lxc_amd64.tar.zst
set -euo pipefail
template=$(realpath "${1:?usage: lxc-template-test.sh template.tar.zst}")
name=fw-lxc-test
image=fleetwarden-lxc-test:latest

cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; docker rmi -f "$image" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup

zstd -dc "$template" | docker import - "$image" >/dev/null
docker run -d --name "$name" --privileged --cgroupns=host \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
    -e container=lxc "$image" /sbin/init >/dev/null
ct() { docker exec "$name" "$@"; }

# Nothing unique may be baked into the template.
ct test ! -e /var/lib/fleetwarden-template/firstboot-done

for _ in $(seq 1 240); do
    ct test -e /var/lib/fleetwarden-template/firstboot-done 2>/dev/null && break
    sleep 1
done
if ! ct test -e /var/lib/fleetwarden-template/firstboot-done; then
    echo "first boot did not finish" >&2
    ct journalctl -u fleetwarden-firstboot --no-pager || true
    ct systemctl --failed --no-pager || true
    exit 1
fi
ct cat /var/log/fleetwarden-firstboot.log

health=$(ct curl -fsS http://127.0.0.1:8080/healthz)
echo "healthz: $health"
[[ "$(jq -r .database <<<"$health")" == ok ]] || { echo "database is not reachable" >&2; exit 1; }
ct grep -q '^FW_LISTEN=0.0.0.0:8080$' /etc/fleetwarden/server.env || { echo "the container does not listen on all interfaces" >&2; exit 1; }
ct systemctl is-enabled fleetwarden-server.service >/dev/null
ct systemctl is-active ssh.service >/dev/null || { echo "ssh did not start" >&2; exit 1; }
[[ -n "$(ct sh -c 'ls /etc/ssh/ssh_host_*_key')" ]] || { echo "no SSH host keys" >&2; exit 1; }
[[ -z "$(ct find /usr/lib/fleetwarden-template -name '*.deb')" ]] || { echo "bundled package was not removed" >&2; exit 1; }
[[ -n "$(ct cat /etc/machine-id)" ]] || { echo "no machine-id was generated" >&2; exit 1; }

# Console screen: URL, username and password only.
issue=$(ct cat /etc/issue)
echo "--- /etc/issue"; echo "$issue"; echo "---"
pw=$(ct sed -n 2p /var/lib/fleetwarden/initial-admin-password)
grep -q 'URL: *http://\\4:8080' <<<"$issue" || { echo "issue has no URL" >&2; exit 1; }
grep -q 'Username: *admin' <<<"$issue" || { echo "issue has no username" >&2; exit 1; }
grep -qF "$pw" <<<"$issue" || { echo "issue has no password" >&2; exit 1; }
[[ "$(grep -c . <<<"$issue")" -eq 3 ]] || { echo "issue shows more than URL, username and password" >&2; exit 1; }

# Signing ct and changing the password removes it from the console screen.
api() { ct curl -fsS -b /tmp/c -c /tmp/c -H 'X-Requested-With: fleetwarden' -H 'Content-Type: application/json' "$@"; }
api -X POST -d "{\"username\":\"admin\",\"password\":\"$pw\"}" http://127.0.0.1:8080/api/v1/auth/login | jq -e '.user.mustChangePassword == true' >/dev/null
api -X POST -d "{\"current\":\"$pw\",\"new\":\"lxc-template-test-password-1\"}" http://127.0.0.1:8080/api/v1/me/password >/dev/null
for _ in $(seq 1 20); do
    ct grep -q 'Password:' /etc/issue || break
    sleep 1
done
if ct grep -q 'Password:' /etc/issue; then echo "password still on the console screen after it was changed" >&2; exit 1; fi
ct grep -q 'URL:' /etc/issue

# A second boot must not run first boot again.
docker restart "$name" >/dev/null
for _ in $(seq 1 60); do
    ct curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break
    sleep 1
done
ct curl -fsS http://127.0.0.1:8080/healthz >/dev/null || { echo "server did not come back after a reboot" >&2; exit 1; }
[[ "$(ct grep -c 'starting' /var/log/fleetwarden-firstboot.log)" -eq 1 ]] || { echo "first boot ran twice" >&2; exit 1; }
echo "LXC template test passed"
