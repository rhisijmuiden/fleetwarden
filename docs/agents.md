# Agents and enrollment

*Nederlands: [agents.nl.md](agents.nl.md)*

The FleetWarden agent runs on every managed machine: as a Windows service
(`FleetWardenAgent`, runs as SYSTEM) or as the systemd service
`fleetwarden-agent` on Debian, Ubuntu and Proxmox VE. Domain-joined and
standalone machines are managed the same way; the domain and OU are only
recorded in the inventory.

## Add a client

Open **Devices > Add client** (or **Groups > Add client** for a specific
group), choose the platform and the group, and click **Create installer**. The
wizard shows:

- **Windows**, in an elevated PowerShell:
  `iwr 'https://em.example.com/agent/install.ps1?t=TOKEN' -UseBasicParsing | iex`
- **Debian / Proxmox VE**, as root:
  `curl -fsSL https://em.example.com/agent/install.sh | sudo bash -s -- TOKEN`
- A **download** of the MSI or .deb with the server address and the token
  built in, so a double-click (or `apt install ./file.deb`) is enough.
- The **msiexec** command line for GPO, Intune or an RMM:
  `msiexec /i fleetwarden-agent.msi /qn SERVER=https://em.example.com TOKEN=TOKEN [GROUP=Workstations]`

The new device shows up in the wizard as soon as it enrolls.

Without the wizard, the generic installers also work interactively: the MSI
wizard asks for the server address and token, and `apt install` asks the same
through debconf. Unattended on Linux:
`sudo FW_SERVER=https://em.example.com FW_TOKEN=TOKEN apt install ./fleetwarden-agent_X_amd64.deb`,
or afterwards `sudo fleetwarden-agent configure --server ... --token ...`.

## Windows installer (MSI)

The MSI installs `C:\Program Files\FleetWarden Agent\fleetwarden-agent.exe`
and the `FleetWardenAgent` service (automatic start, restarts after a crash).
Started without settings, its wizard asks for the server address, the
enrollment token and optionally a device group; **Test connection** checks
that a FleetWarden server answers and shows its TLS certificate. A download
from **Add client** already holds the server and token (the server sets them
with `msitools`, which the server package recommends).

| Property | Meaning |
| --- | --- |
| `SERVER` | Server address, e.g. `https://em.example.com` |
| `TOKEN` | Enrollment token; kept out of the install log |
| `GROUP` | Device group name (optional, overrides the token's group within the token creator's groups) |
| `FORCEENROLL=1` | Replace an existing enrollment on this machine |

Without `SERVER` and `TOKEN` an existing configuration is kept, so an update
is just `msiexec /i fleetwarden-agent_X_x64.msi /qn`. Uninstalling removes the
local enrollment; the device stays in FleetWarden until you delete it. The
wizard is in English for now.

## Enrollment tokens

A token belongs to one device group. It can expire and can be revoked under
**Groups > Enrollment tokens**; revoking it only stops new enrollments.
`GROUP=` (msiexec) or `--group` picks another existing group by name, but
only a group the user who created the token may manage: a token of a
technician limited to some groups cannot place a device in another group (the
token's own group is used and the attempt is in the audit log).
Administrators can also create a token on the server console:
`sudo fleetwarden-server enrollment-token --group Default`.

## Security

- At enrollment the agent creates an Ed25519 key pair; the private key stays
  on the machine (`C:\ProgramData\FleetWarden\agent.json`, readable by SYSTEM
  and Administrators only, or `/etc/fleetwarden-agent/agent.json`, mode 600).
- The agent pins the server's own Ed25519 key. Every message in both
  directions is signed and numbered, so the connection stays authentic even
  when the reverse proxy terminates TLS.
- Agents connect out to the server over HTTPS/WebSocket on port 443
  (`/agent/v1/ws`); nothing listens on the endpoints. See
  [reverse-proxy.md](reverse-proxy.md) for the proxy settings.
- Deleting a device in the web interface makes a connected agent forget its
  enrollment and stop.

## Device groups and roles

Groups set the agent update ring, the agent release channel and an update
window. Technicians and read-only users can be limited to device groups
(**Users & roles**, or per Entra role mapping); administrators always see
every group.

## Scan intervals

**Settings > Agent scans** sets how often agents send their inventory
(hardware, installed software, logged-on user; every 6 hours by default,
15 minutes to 7 days) and how often devices with a patch policy scan for
missing Windows or apt updates (every 6 hours by default, 1 hour to 7 days).
Edit a group under **Groups** to give it its own intervals. Online agents get a
changed inventory interval within a minute; update scans are started by the
server, so a new update scan interval applies right away.

## Agent updates

**Administration > Agent updates** shows the agent versions on the server and
rolls them out:

- Every hour the server checks the GitHub releases for the channels your
  groups use (production groups only take releases, test groups also take
  test builds), downloads the agent packages and checks them against the
  release's `SHA256SUMS`. **Check GitHub now** does the same at once. You can
  also **upload** an `.msi` or `.deb` with its original file name.
- A **rollout** sends a version to the groups ring by ring: ring 0 (pilot)
  first, then rings 1, 2 and 3. A ring runs for the soak time (24 hours by
  default) before the next one starts; rings without devices are skipped. When
  more devices in a ring fail than allowed (2 by default), the rollout pauses.
  You can pause, resume, skip to the next ring, retry failed devices or cancel.
  With **Start a rollout for each new release** on, new releases start a
  rollout by themselves.
- Agents only update inside their group's update window and never downgrade.
- The agent downloads the package with a signed request, checks its SHA-256
  and hands over to a helper process (a copy of itself) that runs `msiexec` or
  `dpkg`. If the install fails or the new version does not connect to the
  server within 5 minutes, the helper reinstalls the previous version and the
  device shows up under **Devices that could not update**.
- Downloaded releases are not part of backups; the server downloads them again.
  The newest 5 are kept, plus every version a device still runs.

## Command line

```
fleetwarden-agent status
fleetwarden-agent inventory          # print what the agent reports
fleetwarden-agent test-connection --server https://em.example.com
fleetwarden-agent configure --server URL --token TOKEN [--group NAME] [--force]
```
