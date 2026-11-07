# FleetWarden

**Self-hosted, agent-based endpoint management for Windows and Linux.**

[Nederlands](README.nl.md)

FleetWarden is a free, source-available alternative to tools such as Action1
and ManageEngine Endpoint Central. It runs entirely on your own Debian 13
server and manages Windows workstations, Windows servers, Debian machines and
Proxmox VE nodes through a lightweight agent.

> **Status:** early development (phase 2: sign-in, roles, mail, updates from the UI, backup and restore). Not ready for production use.

## Planned features

- Hardware and software inventory
- Patch management for Windows (Windows Update takeover, maintenance windows,
  user notifications with deferral) and Debian/Proxmox (apt)
- Software deployment from public repositories (winget) and your own MSI/EXE
  installers
- Browser-based remote control with multi-monitor support and Ctrl+Alt+Del;
  web terminal for Linux
- Reports on outdated software and missing patches, scheduled e-mail reports
- Entra ID single sign-on with role mapping
- Built-in backups (FTP, FTPS, SCP, OneDrive; full and incremental) and restore
- Update the server and agents from the UI, with a production and a test channel
- English and Dutch user interface, fully white-label (logo, colours)

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

- Optional Microsoft Entra ID sign-in: [docs/entra-sso.md](docs/entra-sso.md)
- Updates and channels: [docs/updates.md](docs/updates.md)
- Backup and restore: [docs/backup.md](docs/backup.md)

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
