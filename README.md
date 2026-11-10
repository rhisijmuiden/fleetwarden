# FleetWarden

**Self-hosted, agent-based endpoint management for Windows and Linux.**

[Nederlands](README.nl.md)

FleetWarden is a free, source-available alternative to tools such as Action1
and ManageEngine Endpoint Central. It runs entirely on your own Debian 13
server and manages Windows workstations, Windows servers, Debian machines and
Proxmox VE nodes through a lightweight agent.

> **Status:** version 1.0. Tested with 2,000 simulated agents; see
> [docs/load-test.md](docs/load-test.md) and the security review in
> [docs/hardening.md](docs/hardening.md).

## Features

- **Inventory**: hardware, software, disks, logged-on users, warranty and
  owner; device groups and role-based access limited to groups
- **Patch management**: Windows Update and Debian/Proxmox (apt) with policies,
  approvals, maintenance windows and user notifications with deferral
- **Drivers and firmware** from Windows Update, Dell, HP and Lenovo and fwupd,
  through your own approved repository
- **Software deployment** from winget and your own MSI/EXE/DEB packages,
  uninstalls and forced browser updates
- **Configuration management**: browser policies, drive and printer mappings,
  registry, firewall, power and other Windows settings, scripts
- **Remote control** in the browser, a remote terminal, and management
  without taking over (processes, services, event logs, files)
- **Security status** (BitLocker keys, Defender, firewall, local admins) and
  **vulnerabilities** (CVEs from NVD, CISA KEV, Microsoft and Debian) with a
  fix rollout
- **Power management**: Wake-on-LAN, scheduled restarts and shutdowns
- **Self-service** from the tray app: software catalog, updates, help requests
- **Reports, alerts and asset management** (licences, warranty)
- **Secure gateway** for agents on the internet, **Entra ID** sign-in,
  **backups** (FTP, FTPS, SCP, OneDrive; full and incremental) with restore
- Server and agent **updates from the UI** with a production and a test channel
- English and Dutch user interface, fully white-label (logo, colours)

## Requirements

- Debian 13 (amd64) with 4 vCPU and 8 GB RAM for up to 1,000 endpoints (see
  [sizing](docs/load-test.md#sizing))
- A reverse proxy with TLS (Zoraxy, nginx, Caddy, Traefik, ...)
- Agents: Windows 10/11 and Windows Server 2016 or newer; Debian,
  Ubuntu and Proxmox VE

## Installation

On a clean Debian 13 (amd64) server:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install.sh | sudo bash
```

Add `-s -- --channel test` to install the latest test build. Then put the server
behind your reverse proxy, see [docs/reverse-proxy.md](docs/reverse-proxy.md).

The installer prints a one-time password for the `admin` account; you choose
your own password at the first sign-in. Lost it? Run
`sudo fleetwarden-server reset-admin`.

## Documentation

Every page also exists in Dutch (`.nl.md`).

- Reverse proxy: [docs/reverse-proxy.md](docs/reverse-proxy.md)
- Security and hardening checklist: [docs/hardening.md](docs/hardening.md)
- Optional Microsoft Entra ID sign-in: [docs/entra-sso.md](docs/entra-sso.md)
- Updates and channels: [docs/updates.md](docs/updates.md)
- Backup and restore: [docs/backup.md](docs/backup.md)
- Agents, Add client and enrollment: [docs/agents.md](docs/agents.md)
- Patch management: [docs/patching.md](docs/patching.md)
- Drivers and firmware (own repository): [docs/drivers.md](docs/drivers.md)
- Configurations (policies, mappings, Windows settings, scripts): [docs/configurations.md](docs/configurations.md)
- Security status and BitLocker recovery keys: [docs/security.md](docs/security.md)
- Vulnerabilities (CVEs): [docs/vulnerabilities.md](docs/vulnerabilities.md)
- Software deployment: [docs/software.md](docs/software.md)
- Self-service from the tray app: [docs/selfservice.md](docs/selfservice.md)
- Remote control and terminal: [docs/remote.md](docs/remote.md)
- Power management (Wake-on-LAN, scheduled restarts and shutdowns): [docs/power.md](docs/power.md)
- Management without taking over (processes, services, event logs, files): [docs/manage.md](docs/manage.md)
- Reports and scheduled report mails: [docs/reports.md](docs/reports.md)
- Alerts: [docs/alerts.md](docs/alerts.md)
- Asset management (owner, location, warranty, software licences): [docs/assets.md](docs/assets.md)
- Secure gateway in the DMZ for agents outside the network: [docs/gateway.md](docs/gateway.md)
- Distribution servers in branch offices (cache for installers, drivers and agent updates): [docs/distribution.md](docs/distribution.md)
- Load test and sizing: [docs/load-test.md](docs/load-test.md)
- What changed: [CHANGELOG.md](CHANGELOG.md)

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for the branch model (`test` and `main`),
CI and local development.

## License

FleetWarden is licensed under the **GNU AGPL v3.0 with the Commons Clause**.
You may use, modify and self-host it for free, including inside your company.
Changes must stay open under the same terms. You may **not** sell FleetWarden
or offer a paid product or service whose value derives substantially from it.
See [LICENSE](LICENSE). Because of the Commons Clause this is a
*source-available* license, not an OSI-approved open source license.

Contributions require agreeing to the [CLA](CLA.md).
