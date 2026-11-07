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

## Enrollment tokens

A token belongs to one device group. It can expire and can be revoked under
**Groups > Enrollment tokens**; revoking it only stops new enrollments.
`GROUP=` (msiexec) or `--group` picks another existing group by name.
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

## Command line

```
fleetwarden-agent status
fleetwarden-agent inventory          # print what the agent reports
fleetwarden-agent test-connection --server https://em.example.com
fleetwarden-agent configure --server URL --token TOKEN [--group NAME] [--force]
```
