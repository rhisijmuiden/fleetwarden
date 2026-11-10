#!/usr/bin/env bash
# Installs the server and a distribution server on a clean Debian 13 system,
# enrolls the distribution server with a token from the API and checks that
# an uploaded installer reaches it. Used by CI inside a debian:trixie
# container (no systemd):
#   docker run --rm -v "$PWD:/src" -w /src debian:trixie \
#     tests/install-distribution-test.sh dist/fleetwarden-server_*_amd64.deb dist/distribution/fleetwarden-distribution_*_amd64.deb
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
server_deb="${1:?usage: install-distribution-test.sh server.deb distribution.deb}"
dist_deb="${2:?usage: install-distribution-test.sh server.deb distribution.deb}"

bash installer/install.sh --deb "$server_deb" --yes >/dev/null
set -a
# shellcheck disable=SC1091
. /etc/fleetwarden/server.env
set +a
runuser -u fleetwarden -- /usr/bin/fleetwarden-server >/tmp/server.log 2>&1 &
trap 'kill $(jobs -p) 2>/dev/null || true' EXIT
for _ in $(seq 1 30); do curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break; sleep 1; done

admin_pw=$(sed -n 2p /var/lib/fleetwarden/initial-admin-password)
api() { curl -fsS -b /tmp/cookies -c /tmp/cookies -H 'X-Requested-With: fleetwarden' -H 'Content-Type: application/json' "$@"; }
base=http://127.0.0.1:8080/api/v1
api -X POST -d "{\"username\":\"admin\",\"password\":\"$admin_pw\"}" "$base/auth/login" >/dev/null
api -X POST -d "{\"current\":\"$admin_pw\",\"new\":\"install-test-password-1\"}" "$base/me/password" >/dev/null

# A first install without a token is refused.
if bash installer/install-distribution.sh --deb "$dist_deb" --yes --no-start >/tmp/no-token.log 2>&1; then
    echo "installed without a token" >&2; exit 1
fi

created=$(api -X POST -d '{"name":"Branch","networks":["10.20.0.0/16"],"url":"https://127.0.0.1:8445"}' "$base/distribution")
id=$(jq -r .server.id <<<"$created")
token=$(jq -r .token <<<"$created")
jq -e '.command | contains("install-distribution.sh") and contains("--token")' <<<"$created" >/dev/null || { echo "no install command" >&2; exit 1; }

bash installer/install-distribution.sh --deb "$dist_deb" --server http://127.0.0.1:8080 --token "$token" --listen 127.0.0.1:8445 --yes --no-start | tee /tmp/dist-install.log
grep -q 'Enrolled as "Branch"' /tmp/dist-install.log || { echo "the installer did not enroll" >&2; exit 1; }
[[ "$(stat -c '%a %U %G' /etc/fleetwarden-distribution/distribution.env)" == "640 root fleetwarden-distribution" ]] || { echo "distribution.env has wrong permissions" >&2; exit 1; }
grep -q '^FW_DIST_SERVER=http://127.0.0.1:8080$' /etc/fleetwarden-distribution/distribution.env || { echo "server address not written" >&2; exit 1; }
[[ "$(stat -c '%U' /var/lib/fleetwarden-distribution/identity.json)" == fleetwarden-distribution ]] || { echo "identity owned by the wrong user" >&2; exit 1; }
# The token is used up.
if runuser -u fleetwarden-distribution -- /usr/bin/fleetwarden-distribution enroll --server http://127.0.0.1:8080 --token "$token" 2>/dev/null; then
    echo "the token worked twice" >&2; exit 1
fi

set -a
# shellcheck disable=SC1091
. /etc/fleetwarden-distribution/distribution.env
set +a
runuser -u fleetwarden-distribution -- /usr/bin/fleetwarden-distribution >/tmp/dist.log 2>&1 &
dist_pid=$!
state=""
for _ in $(seq 1 30); do
    state=$(api "$base/distribution" | jq -r ".servers[] | select(.id == $id) | .state")
    [[ "$state" == online ]] && api "$base/distribution" | jq -e ".servers[] | select(.id == $id) | .status != null" >/dev/null && break
    sleep 1
done
[[ "$state" == online ]] || { echo "not online: $state" >&2; cat /tmp/dist.log; exit 1; }
curl -fsSk https://127.0.0.1:8445/healthz | jq -e '.name == "Branch"' >/dev/null || { echo "health check failed" >&2; exit 1; }
# Agents must sign; nothing else is served.
[[ "$(curl -sk -o /dev/null -w '%{http_code}' https://127.0.0.1:8445/agent/v1/software/0000000000000000000000000000000000000000000000000000000000000000)" == 401 ]] ||
    { echo "unsigned download not refused" >&2; exit 1; }
[[ "$(curl -sk -o /dev/null -w '%{http_code}' https://127.0.0.1:8445/api/v1/auth/config)" == 404 ]] || { echo "other paths answered" >&2; exit 1; }

# An uploaded installer reaches the distribution server.
head -c 300000 /dev/urandom >/tmp/app.msi
sum=$(sha256sum /tmp/app.msi | awk '{print $1}')
curl -fsS -b /tmp/cookies -H 'X-Requested-With: fleetwarden' -H 'Content-Type: application/octet-stream' -H 'X-File-Name: Branch App.msi' \
    --data-binary @/tmp/app.msi "$base/software/uploads" >/dev/null
api -X POST -d "{\"name\":\"Branch App\",\"source\":\"upload\",\"fileSha256\":\"$sum\",\"fileName\":\"Branch App.msi\",\"installerKind\":\"msi\"}" "$base/software/packages" >/dev/null
# The distribution server reports every minute; restart it to report now.
kill "$dist_pid" 2>/dev/null || true
sleep 2
runuser -u fleetwarden-distribution -- /usr/bin/fleetwarden-distribution >>/tmp/dist.log 2>&1 &
dist_pid=$!
for _ in $(seq 1 90); do
    [[ -f "/var/lib/fleetwarden-distribution/content/$sum" ]] && break
    sleep 1
done
[[ -f "/var/lib/fleetwarden-distribution/content/$sum" ]] || { echo "file not synced" >&2; cat /tmp/dist.log; exit 1; }
[[ "$(sha256sum "/var/lib/fleetwarden-distribution/content/$sum" | awk '{print $1}')" == "$sum" ]] || { echo "synced file differs" >&2; exit 1; }

# Purging removes the key, the files and the configuration.
kill "$dist_pid" 2>/dev/null || true
sleep 1
apt-get purge -y -qq fleetwarden-distribution >/dev/null
[[ ! -e /etc/fleetwarden-distribution && ! -e /var/lib/fleetwarden-distribution ]] || { echo "purge left files behind" >&2; exit 1; }
echo "Distribution server install test passed."
