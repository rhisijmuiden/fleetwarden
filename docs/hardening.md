# Security and hardening

This page describes how FleetWarden protects itself, what the security review
before version 1.0 found, and what you can do as an operator to harden an
installation further. Dutch version: [hardening.nl.md](hardening.nl.md).

## Security review for 1.0

The review (November 2026) covered the management server, the agent protocol,
the secure gateway, the installer and the packaging. It combined:

- reading the code of every unauthenticated endpoint (login, SSO, agent
  enrollment, agent WebSocket, signed agent downloads and file transfers,
  remote control relays, proxy probes, branding);
- a check that every API route that takes an id enforces the permission and
  the device-group scope of the signed-in user;
- `govulncheck` for the Go standard library and modules, and `npm audit` for
  the web interface;
- a review of the systemd units, file permissions and HTTP headers.

### Findings and fixes

| # | Finding | Severity | Status |
|---|---------|----------|--------|
| 1 | Built with Go 1.26.0: `govulncheck` reported 41 reachable standard library vulnerabilities (net/http, crypto/x509, net/url, os, among others) and two in `golang.org/x/net`. | High | Fixed: Go 1.26.10 and `x/net` 0.62.0. CI now runs `govulncheck` and `npm audit` on every change. |
| 2 | An enrollment token of a technician limited to some device groups could place a device in any group with `GROUP=`, so the device received that group's configurations and software. | Medium | Fixed: the group override only works within the scope of the token's creator; a refused override is audited (`device.enroll.group_denied`). |
| 3 | Every login attempt runs argon2id with 64 MiB of memory; many simultaneous attempts could exhaust the server's memory. | Medium | Fixed: at most four password hashes run at the same time; other attempts wait. |
| 4 | Turning off two-factor authentication asked for the password without a limit on wrong attempts. | Low | Fixed: wrong passwords count towards the per-user login limit. |
| 5 | A file transfer chunk sent without a `Content-Length` was not counted against the maximum transfer size. | Low | Fixed: the size check uses the bytes actually received. |
| 6 | The server unit lacked several systemd sandbox options and a file limit for thousands of agent connections. | Low | Fixed: see the unit below. |
| 7 | Missing `Permissions-Policy`, `Cross-Origin-Opener-Policy` and `Cross-Origin-Resource-Policy` headers. | Info | Fixed. |

### Accepted risks

- **Signed agent HTTP requests can be replayed for five minutes.** The
  signature covers the method, path, device id and time, not the body. The
  requests travel over TLS, are bound to one device and to an open job, so a
  replay needs a broken TLS connection first.
- **Enrollment tokens are bearer secrets.** Anyone with a valid token can
  enroll a device into its group. Give tokens an expiry and a maximum number
  of uses, and revoke tokens you no longer need.
- **The agent runs as root / SYSTEM.** It has to, to install updates and
  software. Everything it executes comes in messages signed with the server
  key that the agent pinned at enrollment.
- **Built-in rate limits are per server process.** They reset on a restart.

## Built-in protections

**Accounts and sessions**

- Passwords: argon2id (64 MiB, 3 passes), at least 12 characters, not
  containing the username. Unknown users cost the same time as wrong passwords.
- Login limits: 20 failures per IP address and 8 per username in 15 minutes.
- Optional TOTP two-factor authentication; codes cannot be reused.
- Sessions: random 256-bit tokens, stored only as a hash; `HttpOnly`,
  `SameSite=Lax` and `Secure` cookies; 2 hours idle and 12 hours absolute.
  Changing your password signs out your other sessions.
- The emergency administrator can be limited to certain networks.
- Roles (administrator, technician, read-only) with device-group scopes; every
  device API checks both.

**Web interface**

- Strict Content Security Policy (no inline scripts), `X-Frame-Options: DENY`,
  `nosniff`, a referrer policy, `Permissions-Policy` and cross-origin
  isolation headers. HSTS can be turned on under *Reverse proxy*.
- Every state-changing API call needs a custom header that browsers cannot
  send cross-site, so cross-site request forgery is blocked.
- Uploaded SVG logos are served with a sandboxing policy.

**Agents**

- Each agent has its own Ed25519 key pair. Enrollment proves that the agent
  holds the private key; the agent pins the server's public key.
- Every WebSocket message is signed and numbered in both directions, so it can
  neither be forged, replayed nor reflected.
- Agent downloads (updates, software, drivers, file transfers) are signed
  requests, and only a device with an open job for the file gets it. Packages
  are checked against their SHA-256 hash before installation.
- Failed agent authentications are rate-limited per IP address.

**Secrets**

- Secrets in the database (SMTP and backup passwords, TOTP secrets, BitLocker
  keys, licence keys) are encrypted with a key in `/var/lib/fleetwarden`.
- Backups are encrypted with your passphrase before they leave the server.

## Hardening checklist for operators

1. **Put the server behind your reverse proxy only.** Keep the default
   `FW_LISTEN=127.0.0.1:8080`, or firewall port 8080 so only the proxy reaches
   it. Set `FW_TRUSTED_PROXIES` to the proxy's address only.
2. **Use HTTPS everywhere** and turn on HSTS under *Administration > Reverse proxy*
   once the certificate works.
3. **Set the public URL** under *Administration > Reverse proxy*, so links and agent
   installers never depend on the `Host` header.
4. **Enable TOTP** for every local account, or use Entra ID sign-in with
   multi-factor authentication. Limit the emergency administrator to your
   management network.
5. **Give technicians a group scope** instead of access to everything.
6. **Use short-lived enrollment tokens** with a maximum number of uses.
7. **Use the secure gateway** for agents on the internet, so the management
   server itself is not exposed.
8. **Configure remote backups** with a strong passphrase and test a restore.
9. **Keep FleetWarden up to date** via *About > Check for updates*; the
   production channel only gets tested releases.
10. **Review the audit log** regularly (*Audit log* in the menu): it records
    every sign-in, failed sign-in and change.

## The server's systemd unit

`fleetwarden-server.service` runs as the unprivileged `fleetwarden` user with
no capabilities, a read-only system (`ProtectSystem=strict`, writes only to
`/var/lib/fleetwarden` and `/var/log/fleetwarden`), private `/tmp` and
devices, no access to home directories, kernel settings, clock or hostname,
no new privileges, only IP, Unix and netlink sockets and a umask of `0027`.
`LimitNOFILE=65536` leaves room for thousands of agent connections.

## Reporting a vulnerability

Please report security problems privately through GitHub's *Report a
vulnerability* button on the repository's *Security* tab, not in a public
issue.
