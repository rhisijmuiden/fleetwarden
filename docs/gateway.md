# Secure gateway (DMZ)

The secure gateway lets agents outside your network (laptops at home, branch
offices without a VPN) reach the management server, **without the management
server being reachable from the internet**. It is a small relay that runs on a
separate machine in your DMZ.

```
agent ──HTTPS 443──▶ gateway (DMZ) ◀──tunnel 8444── management server (LAN)
```

- The **management server opens the tunnel** to the gateway (LAN to DMZ, the
  direction firewalls usually allow). No rule from the DMZ into your network
  is needed.
- The gateway only relays agent traffic (`/agent/...`). The web interface and
  the API are never reachable through it.
- The gateway stores nothing except its own key, the key of the server it is
  paired with and its certificates. No inventory, no passwords, no tokens.
- Agents only trust the server key they pinned at enrollment, and every
  message is signed, so the gateway cannot change or inject messages. Remote
  control sessions stay end-to-end encrypted between the browser's server and
  the agent.

## Requirements

- A Debian 13 machine (amd64 or arm64; a small VM or container with 1 CPU and
  512 MB RAM is plenty) in your DMZ.
- A public DNS name for agents, for example `agents.example.com`.
- Firewall rules:
  - internet → gateway: TCP 443 (or the port of the reverse proxy in front of it);
  - management server → gateway: TCP 8444 (the tunnel). Allow only the server.
  - Let's Encrypt mode also needs TCP 80 from the internet.

## 1. Install the gateway

On the DMZ machine:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install-gateway.sh | sudo bash
# test channel:
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/test/installer/install-gateway.sh | sudo bash -s -- --channel test
```

The installer asks how agents reach the gateway:

| Choice | When | What it configures |
|---|---|---|
| Reverse proxy | You already run Zoraxy, Nginx, Caddy, ... in the DMZ | Plain HTTP on `127.0.0.1:8080` (or an address you choose); the proxy terminates TLS. Set the proxy's IP as trusted proxy. |
| Let's Encrypt | The gateway faces the internet directly | Listens on 443 and 80 and requests a certificate for your domain itself. |
| Own certificate | You have a certificate from your CA | Listens on 443 with your certificate and key. |

It also asks for the IP address of your management server, which is the only
address allowed to open the tunnel (`FW_GW_TUNNEL_ALLOW`).

At the end it shows a **pairing code** such as `K7QF-2MXA-PZ4R-H9TD`. Show it
again at any time with:

```sh
sudo fleetwarden-gateway pairing-code
```

All options can also be passed on the command line, for example
`--tls acme --domain agents.example.com --email it@example.com --tunnel-allow 10.0.10.5 --yes`.
The configuration lives in `/etc/fleetwarden-gateway/gateway.env`.

Behind a reverse proxy, forward WebSockets and allow long-lived connections,
exactly as for the server itself (see [reverse-proxy.md](reverse-proxy.md)). In
Zoraxy, set the WebSocket timeout to 3600 seconds and enable *Refresh timeout
on activity*.

## 2. Pair it in the web interface

*Admin > Secure gateway > Add gateway*:

- **Name**: anything, for example *DMZ Amsterdam*.
- **Tunnel address**: how the management server reaches the gateway, such as
  `10.0.20.5` or `gw.dmz.lan:8444` (port 8444 unless you changed it).
- **Public URL for agents**: `https://agents.example.com`.
- **Pairing code**: from step 1. It works once.

The server connects, both sides prove their keys and pin each other, and the
gateway shows as *Connected*. **Test** checks the tunnel and that the public
URL really leads through this gateway to this server. (When the server cannot
reach its own public address, for example without hairpin NAT, the second check
fails from the inside; test from a device outside the network.)

## 3. Agents

Every agent gets the list of server addresses, in this order:

1. the server's own external base URL (*Settings* or *Reverse proxy*);
2. each enabled gateway.

An agent tries them in order and moves on when one does not answer, so a laptop
uses the internal address in the office and the gateway at home without any
change. Existing agents receive the list automatically on their next connection.
The device page shows *via gateway ...* when a device is connected through one.

To install agents on devices that are outside the network now, pick the gateway
under **Connect via** in the *Add client* wizard: the commands and downloads then
point at the gateway.

## Updates

The gateway shows its version on the gateway page. When it differs from the
server's, **Update gateway** asks the gateway to install the server's version
from the official GitHub release (checksums verified). The tunnel drops for a
few seconds while it restarts. Running the install command again also updates
it and keeps the configuration and pairing.

## Pairing again

After reinstalling the gateway or to move it to another server:

```sh
sudo fleetwarden-gateway reset-pairing
```

This forgets the paired server, keeps the gateway key and shows a new code.
Then use **Pair again** on the gateway page and enter the new code.

## Security notes

- The tunnel uses TLS with a self-signed certificate generated on the gateway.
  The handshake signs the certificate's hash with both keys, so a
  man-in-the-middle with another certificate cannot complete it. You may set
  your own tunnel certificate with `FW_GW_TUNNEL_CERT` and `FW_GW_TUNNEL_KEY`.
- The tunnel must reach the gateway directly (no TLS-terminating proxy in
  between), because of that certificate binding.
- Five wrong pairing codes lock pairing for 15 minutes.
- The gateway runs as an unprivileged user with a hardened systemd unit; only
  its updater runs as root, and it accepts nothing but a version number.

## Configuration reference (`/etc/fleetwarden-gateway/gateway.env`)

| Variable | Default | Meaning |
|---|---|---|
| `FW_GW_TLS` | `proxy` | `proxy`, `files` or `acme` |
| `FW_GW_LISTEN` | `127.0.0.1:8080` (proxy) / `:443` | Agent listener |
| `FW_GW_TRUSTED_PROXIES` | `127.0.0.1/32,::1/128` | Proxies whose `X-Forwarded-For` is trusted (proxy mode) |
| `FW_GW_TLS_CERT`, `FW_GW_TLS_KEY` | | Certificate and key (files mode) |
| `FW_GW_ACME_DOMAINS`, `FW_GW_ACME_EMAIL` | | Let's Encrypt (acme mode) |
| `FW_GW_TUNNEL_LISTEN` | `:8444` | Tunnel listener |
| `FW_GW_TUNNEL_ALLOW` | (any) | Addresses allowed to open the tunnel |
| `FW_GW_TUNNEL_CERT`, `FW_GW_TUNNEL_KEY` | generated | Own tunnel certificate |

Logs: `journalctl -u fleetwarden-gateway`. Health check for monitoring:
`GET /healthz` on the agent listener answers `200` with `"server":"connected"`,
or `503` while the management server is not connected.
