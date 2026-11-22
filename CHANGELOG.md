# Changelog

All notable changes to FleetWarden. Dutch version: [CHANGELOG.nl.md](CHANGELOG.nl.md).
Versions follow [semantic versioning](https://semver.org/); test builds are
published as `X.Y.Z-test.N` before a release.

## Unreleased

### Added
- MCP server for AI assistants such as Claude (`/mcp`, off by default):
  37 tools to look up and manage devices, updates, vulnerabilities, security,
  alerts, software and assets. Assistants act as a FleetWarden user (role and
  device groups apply), every call is in the audit log, access levels read /
  act / full (destructive tools need the host name as confirmation). Personal
  tokens and OAuth 2.1 sign-in for claude.ai. Administration > AI assistants
  (MCP); own tokens under Profile. See docs/mcp.md.
- Proxmox LXC container template (Debian 13, `.tar.zst`). Download it under
  CT Templates > Download from URL, create a container and FleetWarden runs:
  the first start creates the SSH keys, database and admin account, and the
  console shows only the URL, username and one-time password. Built and boot
  tested in CI; the test channel has a fixed URL (release `lxc-test`),
  production uses `releases/latest/download/`. See docs/install.md.
- install.sh: `--listen` and `--trusted-proxies` for a reverse proxy on another
  machine, and a check whether it runs on a VM, physical machine or container
  with warnings for too little memory or disk.

### Changed
- New house style: IJmond360 (purple, pink and aubergine, Inter with Epilogue
  for page titles, the IJmond360 logo). It replaces the blue FleetWarden style in
  the web interface, e-mails, PDF reports and the Windows tray pop-up. The old
  style stays in branding/fleetwarden/. White-label colours set under
  Administration > Branding still override it.

### Fixed
- Approved agent versions did not reach agents on 1.0.0: test builds were
  still numbered 1.0.0-test.N, which sorts below the release 1.0.0, so those
  agents counted as up to date. The version is now 1.1.0, so test builds are
  1.1.0-test.N. The approve card says how many agents a version will update
  and why others stay as they are (a newer version, or another channel).

### Added
- Agent updates by approval: Agent updates lists the 5 latest agent versions;
  approve one and every agent with an older version updates to it, without
  rollout rings. This is now the default; rolling out ring by ring stays
  available as a setting. See docs/agents.md.
- Chat with the signed-on user: a chat panel on the device page and in help
  requests, a chat window in the tray, delivery state, automatic end after an
  hour, and the transcript as a note on the linked service desk ticket.
  Unattended takeover per device group: remote control connects when nobody
  answers the consent prompt. Both are ticket link actions. See
  docs/selfservice.md and docs/remote.md.
- Tickets from the tray: the help form becomes a ticket form with a required
  description, urgency and category. A ticket goes to FleetWarden, the user's
  own Outlook (with the device report attached), the mail server or a service
  desk integration, with a device report (hardware, IP, reboot pending,
  waiting updates, catalog software, security, alerts) and a link that opens
  the device in FleetWarden with remote control, terminal or scan. Ticket
  number and state from the service desk, replies as notes, a preview in the
  settings, and deep links that survive the sign-in. See docs/selfservice.md.
- Integrations (Administration > Integrations): connectors for TOPdesk,
  Freshservice, Jira Service Management, ServiceNow, Zendesk, HaloITSM, Zammad
  and GLPI (ticket with requester lookup, priority mapping, private notes,
  status), and custom API integrations with a method, URL, authentication
  (bearer, Basic, API key, OAuth 2.0 client credentials, HMAC), headers and a
  body template with variables. New alerts and new devices can trigger them;
  test connection, test ticket and a 30-day call log. See docs/integrations.md.
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
