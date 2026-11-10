# Changelog

All notable changes to FleetWarden. Dutch version: [CHANGELOG.nl.md](CHANGELOG.nl.md).
Versions follow [semantic versioning](https://semver.org/); test builds are
published as `X.Y.Z-test.N` before a release.

## Unreleased

### Added
- Distribution servers for branch offices (Admin > Distribution servers): a
  cache that keeps uploaded installers, drivers and firmware and agent
  packages, and serves them to the agents in its networks or device groups.
  The management server still decides per download; agents pin its
  certificate, check every file's SHA-256 and fall back to the management
  server. Installed with a one-time token, optional bandwidth limit, update
  from the web interface. New package `fleetwarden-distribution` and
  `install-distribution.sh`; see docs/distribution.md.

## 1.0.0 (2026-11)

The first production release. Everything below was built and tested on the
test channel in the 0.1 series.

### Platform
- Debian 13 installer, systemd services, updates of the server and agents from
  the web interface with a production and a test channel, automatic rollback.
- Local accounts with argon2id, TOTP, roles (administrator, technician,
  read-only) limited to device groups, emergency administrator; optional
  Microsoft Entra ID sign-in with role mapping.
- Own SMTP server settings (STARTTLS/TLS, mail log with retry), branding and
  full white-label, English and Dutch interface, audit log.
- Backups to FTP, FTPS, SCP (SFTP) and OneDrive, full and incremental,
  encrypted, with restore from the remote location.
- Reverse proxy page with generated configurations and an end-to-end test;
  secure gateway for agents on the internet.

### Endpoints
- Windows agent (MSI with wizard, silent install, tray app) and Linux agent
  (.deb, Debian/Ubuntu/Proxmox VE); Add client wizard with enrollment tokens;
  agent updates in rings with rollback; configurable scan intervals.
- Hardware and software inventory, device groups, asset management (owner,
  location, warranty, software licences).

### Management
- Patch management for Windows Update and apt with policies, approvals,
  maintenance windows and user notifications.
- Driver and firmware repository (Windows Update, Dell, HP, Lenovo, fwupd,
  own packages), only after approval.
- Software deployment from winget and own packages, uninstall, forced browser
  updates with session restore.
- Configuration management: browser policies, registry, scripts, drive and
  printer mappings, Windows settings, profiles per group.
- Remote control in the browser, remote terminal, processes, services, event
  logs and file transfer without taking over the screen.
- Power management: Wake-on-LAN, scheduled restarts and shutdowns.
- Self-service from the tray app: software catalog, updates, help requests.

### Insight
- Security status: BitLocker recovery keys, Defender, firewall, local admins.
- Vulnerabilities (NVD, CISA KEV, Microsoft, Debian) with fix rollout and
  accepted risks.
- Reports with PDF/CSV export and scheduled mails; alerts.

### Hardening for 1.0 (phase 17)
- Security review, see [docs/hardening.md](docs/hardening.md): Go 1.26.10
  (41 standard library vulnerabilities fixed), `GROUP=` on enrollment limited
  to the token creator's groups, a limit on parallel password hashing, a rate
  limit on turning off TOTP, stricter systemd sandboxing, extra security
  headers, and govulncheck and npm audit in CI.
- Load test with 2,000 simulated agents, see [docs/load-test.md](docs/load-test.md):
  fixed agents that could drop their connection when the server sent several
  messages at once, server memory with 2,000 agents down from 3.1 GB to
  0.2 GB, vulnerability pages up to seven times faster, database pool and
  index tuning.
- Documentation complete in English and Dutch.
