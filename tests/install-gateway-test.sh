#!/usr/bin/env bash
# Installs the server and the secure gateway packages on a clean Debian 13
# system, pairs them through the API and checks that agents reach the server
# through the gateway. Used by CI inside a debian:trixie container (no systemd):
#   docker run --rm -v "$PWD:/src" -w /src debian:trixie \
#     tests/install-gateway-test.sh dist/fleetwarden-server_*_amd64.deb dist/gateway/fleetwarden-gateway_*_amd64.deb
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
server_deb="${1:?usage: install-gateway-test.sh server.deb gateway.deb}"
gateway_deb="${2:?usage: install-gateway-test.sh server.deb gateway.deb}"

bash installer/install.sh --deb "$server_deb" --yes >/dev/null
set -a
# shellcheck disable=SC1091
. /etc/fleetwarden/server.env
set +a
runuser -u fleetwarden -- /usr/bin/fleetwarden-server >/tmp/server.log 2>&1 &
for _ in $(seq 1 30); do curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break; sleep 1; done

# The gateway: behind a "reverse proxy" on 127.0.0.1:8090, tunnel on 8444.
bash installer/install-gateway.sh --deb "$gateway_deb" --tls proxy --listen 127.0.0.1:8090 --tunnel-allow 127.0.0.1 --yes | tee /tmp/gw-install.log
grep -q 'Pairing code:' /tmp/gw-install.log || { echo "the installer did not show a pairing code" >&2; exit 1; }
[[ "$(stat -c '%a %U %G' /etc/fleetwarden-gateway/gateway.env)" == "640 root fleetwarden-gateway" ]] || { echo "gateway.env has wrong permissions" >&2; exit 1; }
grep -q '^FW_GW_LISTEN=127.0.0.1:8090$' /etc/fleetwarden-gateway/gateway.env || { echo "listen address not written" >&2; exit 1; }
grep -q '^FW_GW_TUNNEL_ALLOW=127.0.0.1$' /etc/fleetwarden-gateway/gateway.env || { echo "tunnel allow list not written" >&2; exit 1; }
code=$(runuser -u fleetwarden-gateway -- /usr/bin/fleetwarden-gateway pairing-code | awk '/Pairing code/{print $3}')
[[ -n "$code" ]] || { echo "no pairing code" >&2; exit 1; }

set -a
# shellcheck disable=SC1091
. /etc/fleetwarden-gateway/gateway.env
set +a
runuser -u fleetwarden-gateway -- /usr/bin/fleetwarden-gateway >/tmp/gateway.log 2>&1 &
gw_pid=$!
trap 'kill $(jobs -p) 2>/dev/null || true' EXIT
for _ in $(seq 1 30); do curl -sS http://127.0.0.1:8090/healthz >/dev/null 2>&1 && break; sleep 1; done
curl -sS http://127.0.0.1:8090/healthz | jq -e '.server == "disconnected"' >/dev/null

# Sign in, change the one-time password and pair the gateway.
admin_pw=$(sed -n 2p /var/lib/fleetwarden/initial-admin-password)
api() { curl -fsS -b /tmp/cookies -c /tmp/cookies -H 'X-Requested-With: fleetwarden' -H 'Content-Type: application/json' "$@"; }
base=http://127.0.0.1:8080/api/v1
api -X POST -d "{\"username\":\"admin\",\"password\":\"$admin_pw\"}" "$base/auth/login" >/dev/null
api -X POST -d "{\"current\":\"$admin_pw\",\"new\":\"install-test-password-1\"}" "$base/me/password" >/dev/null
gw_id=$(api -X POST -d "{\"name\":\"DMZ\",\"address\":\"127.0.0.1:8444\",\"publicUrl\":\"http://127.0.0.1:8090\",\"pairingCode\":\"$code\"}" "$base/gateways" | jq -r .id) ||
    { cat /tmp/gateway.log; exit 1; }
state=""
for _ in $(seq 1 30); do
    state=$(api "$base/gateways" | jq -r ".gateways[] | select(.id == $gw_id) | .state")
    [[ "$state" == connected ]] && break
    sleep 1
done
[[ "$state" == connected ]] || { echo "tunnel not connected: $state" >&2; cat /tmp/gateway.log /tmp/server.log; exit 1; }
runuser -u fleetwarden-gateway -- /usr/bin/fleetwarden-gateway pairing-code | grep -q 'Paired with' || { echo "the gateway did not pin the server" >&2; exit 1; }

# Agents reach the server through the gateway; nothing else passes.
server_key=$(curl -fsS http://127.0.0.1:8080/agent/v1/hello | jq -r .serverPublicKey)
[[ "$(curl -fsS http://127.0.0.1:8090/agent/v1/hello | jq -r .serverPublicKey)" == "$server_key" ]] || { echo "hello through the gateway differs" >&2; exit 1; }
[[ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8090/api/v1/auth/config)" == 404 ]] || { echo "the API passes the gateway" >&2; exit 1; }
api -X POST "$base/gateways/$gw_id/test" | jq -e '.checks | all(.ok)' >/dev/null || { echo "gateway test failed" >&2; api -X POST "$base/gateways/$gw_id/test"; exit 1; }

# Install commands through the gateway point at the gateway.
group=$(api "$base/groups" | jq -r '.[0].id')
tok=$(api -X POST -d "{\"groupId\":$group,\"description\":\"gateway test\",\"expiresInDays\":1}" "$base/enrollment-tokens" | jq -r .id)
info=$(api "$base/enrollment-tokens/$tok/install?via=$gw_id")
token=$(jq -r .token <<<"$info")
jq -e '.linuxCommand | contains("http://127.0.0.1:8090/agent/install.sh")' <<<"$info" >/dev/null || { echo "install command does not use the gateway" >&2; exit 1; }
curl -fsS http://127.0.0.1:8090/agent/install.sh | grep -q 'http://127.0.0.1:8090' || { echo "install script does not point at the gateway" >&2; exit 1; }

# A Linux agent enrolls and connects through the gateway.
shopt -s nullglob
agent_debs=(dist/agent/fleetwarden-agent_*_amd64.deb)
shopt -u nullglob
if [[ ${#agent_debs[@]} -gt 0 ]]; then
    agent_deb="${agent_debs[0]}"
    apt-get install -y -qq "$(realpath "$agent_deb")" >/dev/null
    fleetwarden-agent configure --server http://127.0.0.1:8090 --token "$token"
    fleetwarden-agent run >/tmp/agent.log 2>&1 &
    via=""
    for _ in $(seq 1 30); do
        via=$(api "$base/devices" | jq -r '.items[0].connectedVia // empty')
        [[ "$via" == DMZ ]] && break
        sleep 1
    done
    [[ "$via" == DMZ ]] || { echo "agent not connected through the gateway" >&2; cat /tmp/agent.log /tmp/gateway.log; exit 1; }
    api "$base/gateways" | jq -e ".gateways[] | select(.id == $gw_id) | .agents == 1" >/dev/null
fi

# Reset on the gateway: the server reports the refusal; pairing again works.
kill "$gw_pid" 2>/dev/null || true
sleep 1
runuser -u fleetwarden-gateway -- /usr/bin/fleetwarden-gateway reset-pairing >/tmp/reset.log
new_code=$(awk '/Pairing code/{print $3}' /tmp/reset.log)
runuser -u fleetwarden-gateway -- /usr/bin/fleetwarden-gateway >>/tmp/gateway.log 2>&1 &
sleep 2
api -X POST -d "{\"pairingCode\":\"$new_code\"}" "$base/gateways/$gw_id/pair" >/dev/null || { cat /tmp/gateway.log; exit 1; }
for _ in $(seq 1 30); do
    state=$(api "$base/gateways" | jq -r ".gateways[] | select(.id == $gw_id) | .state")
    [[ "$state" == connected ]] && break
    sleep 1
done
[[ "$state" == connected ]] || { echo "not connected after pairing again: $state" >&2; exit 1; }

# Purging removes the key and configuration.
apt-get purge -y -qq fleetwarden-gateway >/dev/null
[[ ! -e /etc/fleetwarden-gateway && ! -e /var/lib/fleetwarden-gateway ]] || { echo "purge left files behind" >&2; exit 1; }
echo "Gateway install test passed."
