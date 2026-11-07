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

# Re-installing (upgrade path) must keep the existing configuration.
before=$(sha256sum /etc/fleetwarden/server.env)
apt-get install -y -qq --reinstall "$(realpath "$deb")" >/dev/null
[[ "$(sha256sum /etc/fleetwarden/server.env)" == "$before" ]] || { echo "configuration changed on reinstall" >&2; exit 1; }

echo "Install test passed"
