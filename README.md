# FleetWarden

**Self-hosted, agent-based endpoint management for Windows and Linux.**

[Nederlands](README.nl.md)

FleetWarden is a free, source-available alternative to tools such as Action1
and ManageEngine Endpoint Central. It runs entirely on your own Debian 13
server and manages Windows workstations, Windows servers, Debian machines and
Proxmox VE nodes through a lightweight agent.

> **Status:** early development (phase 0, foundation). Not ready for production use.

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

## License

FleetWarden is licensed under the **GNU AGPL v3.0 with the Commons Clause**.
You may use, modify and self-host it for free, including inside your company.
Changes must stay open under the same terms. You may **not** sell FleetWarden
or offer a paid product or service whose value derives substantially from it.
See [LICENSE](LICENSE). Because of the Commons Clause this is a
*source-available* license, not an OSI-approved open source license.

Contributions require agreeing to the [CLA](CLA.md).
