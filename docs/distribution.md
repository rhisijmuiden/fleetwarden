# Distribution servers (branch offices)

A distribution server is a cache in a branch office. Every approved installer,
driver or firmware file and agent update crosses the (slow) link to the
management server **once**; the agents in that office then download it from
the distribution server on the local network.

```
management server ──once per file──▶ distribution server (branch) ──LAN──▶ agents
```

- The distribution server **connects out** to the management server, at the
  same address the agents use (through your reverse proxy or a secure
  gateway). Nothing has to reach it from the internet.
- It keeps every uploaded installer, the drivers and firmware in the
  repository and the agent packages. Files that are no longer offered are
  removed.
- **Who gets what is still decided by the management server.** For every
  download the distribution server forwards the agent's signed request to the
  management server and only serves the file it names; an agent without an
  open job for a file gets nothing, exactly as without a distribution server.
- **Agents check every file.** They compare the size and SHA-256 the management
  server gave them, and pin the distribution server's certificate (the
  management server passes its fingerprint), so a distribution server cannot
  hand out anything else.
- **Agents fall back** to the management server when the distribution server is
  offline, refuses, or does not have the file yet. A file an agent asked for
  is fetched first.

What is not cached: Windows Update content (agents download it from
Microsoft), apt packages for Linux and winget installers, which come from
their vendors' download sites. Only files that live on the management server
are distributed.

## Requirements

- A Debian 13 machine (amd64 or arm64) in the office: a small VM with 1 CPU,
  1 GB RAM and enough disk space for your installers and drivers. The page
  shows the total size of what is distributed.
- The machine reaches the management server at the agents' address.
- The agents in the office reach the machine on TCP **8445** (HTTPS).

## Setting one up

1. Open **Admin > Distribution servers** and choose **Add distribution
   server**. Give it a name and say which agents use it:
   - **Networks**: CIDRs such as `10.20.0.0/16`, or single addresses. An agent
     matches with one of its local addresses or with the public address the
     management server sees, so an office behind NAT can be set up with its
     public IP (`203.0.113.7`).
   - **Device groups**: devices in these groups use the server wherever they
     are.
   When several servers match, the one with the most specific network comes
   first (an agent tries up to three).
2. Optionally set a **download limit** in Mbit/s so syncing does not fill the
   office's internet connection.
3. Copy the **install command** that appears and run it as root on the
   machine. It installs the matching version, enrolls with the one-time token
   (valid for 24 hours) and starts the service. Lost it? Choose **New install
   command**.
4. The server appears as **Online** and starts downloading. The address agents
   use is filled in from the address it reports (`https://<ip>:8445`); edit it
   to use a DNS name.

```
curl -fsSL https://github.com/rhisijmuiden/fleetwarden/releases/download/v1.1.0/install-distribution.sh \
  | sudo bash -s -- --server https://fleet.example.com --token fwd_… --version 1.1.0
```

Installer options: `--listen ADDR` (default `:8445`), `--content-dir DIR` to
store the files on another disk, `--min-free-gb N` (default 2: it stops
downloading before the disk is full), `--deb FILE` for a local package.

## Day to day

The page shows per server: files stored and still to go, what it is
downloading, disk space, how many agents use it, how many files it served,
requests for files it did not have yet and requests the management server
refused, and the last report (every minute).

- **Disable** sends the agents back to the management server; the files stay.
- **Update to …** appears when the server runs another version than the
  management server; it installs the same release from GitHub, verifies
  `SHA256SUMS` and rolls back when the new version does not start (as for the
  gateway). You can also run the installer again.
- **Delete** forgets the server. Remove it from the machine with
  `apt purge fleetwarden-distribution`.

On the machine:

| | |
|---|---|
| Configuration | `/etc/fleetwarden-distribution/distribution.env` |
| Files | `/var/lib/fleetwarden-distribution/content/<sha256>` |
| Logs | `journalctl -u fleetwarden-distribution` |
| Enrollment | `sudo fleetwarden-distribution status` |
| Enroll again | `sudo fleetwarden-distribution enroll --server URL --token TOKEN` |

Settings in `distribution.env`: `FW_DIST_SERVER`, `FW_DIST_LISTEN`,
`FW_DIST_CONTENT_DIR`, `FW_DIST_MIN_FREE_GB`, `FW_DIST_TLS_CERT` and
`FW_DIST_TLS_KEY` (your own certificate; agents pin it as well),
`FW_DIST_CA_FILE` (an internal CA for the management server's certificate).

## Security

- The distribution server has its own Ed25519 key; every request to the
  management server is signed with it. The one-time token only works once.
- It stores no inventory, passwords or tokens: only the files and its key.
- The service runs as its own user with systemd sandboxing and can only write
  its data directory.
- A stolen distribution server can read the distributed installers (as any
  agent with a job could) but cannot get files the management server does not
  hand out, and cannot change what agents install.
