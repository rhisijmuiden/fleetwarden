#!/usr/bin/env bash
# Installs the server package on a clean Debian 13 system and checks that it runs.
# Used by CI inside a debian:trixie container (no systemd):
#   docker run --rm -v "$PWD:/src" -w /src debian:trixie tests/install-test.sh dist/fleetwarden-server_*.deb
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
deb="${1:?usage: install-test.sh path/to/fleetwarden-server.deb}"

bash installer/install.sh --deb "$deb" --yes

test -f /etc/fleetwarden/server.env || { echo "server.env was not created" >&2; exit 1; }
[[ "$(stat -c '%a %U %G' /etc/fleetwarden/server.env)" == "640 root fleetwarden" ]] || { echo "server.env has wrong permissions" >&2; exit 1; }

# Without systemd the service is not started by the package; start it the same way the unit does.
set -a
# shellcheck disable=SC1091
. /etc/fleetwarden/server.env
set +a
runuser -u fleetwarden -- /usr/bin/fleetwarden-server >/tmp/server.log 2>&1 &
trap 'kill %1 2>/dev/null || true' EXIT

for _ in $(seq 1 30); do
    curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break
    sleep 1
done
health=$(curl -fsS http://127.0.0.1:8080/healthz) || { cat /tmp/server.log; exit 1; }
echo "healthz: $health"
[[ "$(jq -r .database <<<"$health")" == "ok" ]] || { echo "database is not reachable" >&2; cat /tmp/server.log; exit 1; }
curl -fsS http://127.0.0.1:8080/api/v1/version | jq -e '.version' >/dev/null
curl -fsS http://127.0.0.1:8080/ | grep -q '<div id="root">' || { echo "web interface not served" >&2; exit 1; }
script=$(curl -fsS http://127.0.0.1:8080/ | grep -o '/assets/index-[^"]*\.js' | head -n1)
[[ -n "$script" ]] || { echo "no script tag in index.html" >&2; exit 1; }
curl -fsS -o /dev/null -w '%{content_type}' "http://127.0.0.1:8080$script" | grep -q javascript || { echo "web assets are not served ($script)" >&2; exit 1; }

# The Debian version of a test build must sort below the release (0.1.0~test.N < 0.1.0).
pkg_version=$(dpkg-query -W -f '${Version}' fleetwarden-server)
echo "package version: $pkg_version"
if [[ "$(/usr/bin/fleetwarden-server -version)" == *-test.* ]]; then
    [[ "$pkg_version" == *"~test."* ]] || { echo "test build has Debian version $pkg_version, expected X.Y.Z~test.N" >&2; exit 1; }
fi

# First login: one-time password from the installer, forced password change.
pw_file=/var/lib/fleetwarden/initial-admin-password
[[ "$(stat -c '%a %U' "$pw_file")" == "600 fleetwarden" ]] || { echo "initial password file missing or wrong permissions" >&2; exit 1; }
admin_pw=$(sed -n 2p "$pw_file")
api() { curl -fsS -b /tmp/cookies -c /tmp/cookies -H 'X-Requested-With: fleetwarden' -H 'Content-Type: application/json' "$@"; }
api -X POST -d "{\"username\":\"admin\",\"password\":\"$admin_pw\"}" http://127.0.0.1:8080/api/v1/auth/login | jq -e '.user.mustChangePassword == true' >/dev/null
code=$(curl -s -o /dev/null -w '%{http_code}' -b /tmp/cookies http://127.0.0.1:8080/api/v1/users)
[[ "$code" == 403 ]] || { echo "API usable before the forced password change ($code)" >&2; exit 1; }
api -X POST -d "{\"current\":\"$admin_pw\",\"new\":\"install-test-password-1\"}" http://127.0.0.1:8080/api/v1/me/password >/dev/null
api http://127.0.0.1:8080/api/v1/users | jq -e 'length == 1' >/dev/null
[[ ! -e "$pw_file" ]] || { echo "initial password file not removed after the password change" >&2; exit 1; }

# Backup to a local folder, then restore it with the restore helper (as the
# fleetwarden-restore.service would, but without systemd).
base=http://127.0.0.1:8080/api/v1
api -X PUT -d '{"new":"install-test-passphrase"}' "$base/backup/passphrase" >/dev/null
loc_id=$(api -X POST -d '{"name":"Local","type":"local","path":"install-test","enabled":true}' "$base/backup/locations" | jq -r .id)
wait_backup() {
    for _ in $(seq 1 120); do
        p=$(api "$base/backup/progress")
        [[ "$(jq -r .running <<<"$p")" == "false" ]] && { echo "$p"; return 0; }
        sleep 1
    done
    echo "backup did not finish" >&2; return 1
}
api -X POST -d '{"kind":"full"}' "$base/backup/run" >/dev/null
progress=$(wait_backup)
[[ "$(jq -r ".locations[\"$loc_id\"].state" <<<"$progress")" == succeeded ]] || { echo "backup failed: $progress" >&2; cat /tmp/server.log; exit 1; }
# The command line backup runs an incremental one.
fleetwarden-server backup | grep -q 'Backup completed' || { echo "command line backup failed" >&2; exit 1; }
api -X POST -d '{"username":"after-backup","role":"readonly"}' "$base/users" >/dev/null
api "$base/users" | jq -e 'length == 2' >/dev/null
snap=$(api -X POST -d "{\"locationId\":$loc_id}" "$base/backup/snapshots" | jq -c '[.[] | select(.kind == "full")][0]')
api -X POST -d "{\"locationId\":$loc_id,\"chain\":$(jq .chain <<<"$snap"),\"snapshot\":$(jq .id <<<"$snap"),\"confirm\":\"RESTORE\"}" "$base/backup/restore" >/dev/null
for _ in $(seq 1 60); do
    state=$(api "$base/backup/restore" | jq -r .state)
    [[ "$state" == requested || "$state" == failed ]] && break
    sleep 1
done
[[ "$state" == requested ]] || { echo "restore was not staged: $(api "$base/backup/restore")" >&2; exit 1; }
kill %1; wait %1 2>/dev/null || true
fleetwarden-server restore-apply
jq -e '.state == "completed"' /var/lib/fleetwarden-update/restore-status.json >/dev/null || { cat /var/lib/fleetwarden-update/restore-status.json; exit 1; }
[[ ! -e /var/lib/fleetwarden/restore/request.json ]] || { echo "restore request not removed" >&2; exit 1; }
runuser -u fleetwarden -- /usr/bin/fleetwarden-server >/tmp/server.log 2>&1 &
for _ in $(seq 1 30); do curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break; sleep 1; done
rm -f /tmp/cookies
api -X POST -d '{"username":"admin","password":"install-test-password-1"}' "$base/auth/login" >/dev/null || { echo "login after restore failed" >&2; cat /tmp/server.log; exit 1; }
api "$base/users" | jq -e 'length == 1' >/dev/null || { echo "restore did not bring back the old accounts" >&2; exit 1; }
api "$base/audit?q=backup.restore_result" | jq -e '.[0].success == true' >/dev/null || { echo "restore result not audited" >&2; exit 1; }

# Agent: enroll this machine with the one-liner from the Add client wizard,
# using the agent package bundled with the server.
ls /usr/share/fleetwarden/agent/fleetwarden-agent_*_amd64.deb >/dev/null || { echo "no bundled agent package" >&2; exit 1; }
token=$(fleetwarden-server enrollment-token --group Default)
[[ "$token" == fwe_* ]] || { echo "enrollment-token printed '$token'" >&2; exit 1; }
curl -fsSL http://127.0.0.1:8080/agent/install.sh | bash -s -- "$token"
[[ "$(stat -c '%a' /etc/fleetwarden-agent/agent.json)" == 600 ]] || { echo "agent config has wrong permissions" >&2; exit 1; }
fleetwarden-agent status | grep -q 'Waiting to enroll' || { echo "agent was not configured from the baked package" >&2; exit 1; }
# No systemd here: run one agent cycle in the foreground (enroll + inventory).
fleetwarden-agent once || { cat /tmp/server.log; exit 1; }
fleetwarden-agent status | grep -q 'Enrolled as device' || { echo "agent did not enroll" >&2; exit 1; }
devices=$(api "$base/devices")
echo "devices: $(jq -c '.items[0] | {hostname, platform, osName, groupName, inventoryAt}' <<<"$devices")"
jq -e '.total == 1 and .items[0].platform == "linux" and .items[0].inventoryAt != null' <<<"$devices" >/dev/null || { echo "device not registered: $devices" >&2; exit 1; }
dev_id=$(jq -r '.items[0].id' <<<"$devices")
api "$base/devices/$dev_id/software" | jq -e 'length > 10 and any(.[]; .name == "fleetwarden-agent")' >/dev/null || { echo "software inventory missing" >&2; exit 1; }

# Re-installing (upgrade path) must keep the configuration, the secret key and the accounts.
before=$(sha256sum /etc/fleetwarden/server.env /var/lib/fleetwarden/secret.key)
apt-get install -y -qq --reinstall "$(realpath "$deb")" >/dev/null
[[ "$(sha256sum /etc/fleetwarden/server.env /var/lib/fleetwarden/secret.key)" == "$before" ]] || { echo "configuration changed on reinstall" >&2; exit 1; }
[[ ! -e "$pw_file" ]] || { echo "reinstall created a new admin password" >&2; exit 1; }

# Console recovery of the emergency administrator.
fleetwarden-server reset-admin | grep -q 'password:' || { echo "reset-admin failed" >&2; exit 1; }
[[ -s "$pw_file" ]] || { echo "reset-admin did not write the password file" >&2; exit 1; }

echo "Install test passed"
