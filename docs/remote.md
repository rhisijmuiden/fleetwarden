# Remote control

*Nederlands: [remote.nl.md](remote.nl.md)*

FleetWarden can take over the screen, mouse and keyboard of Windows servers
and clients from the browser. Nothing has to be installed besides the agent:
its service (`FleetWardenAgent`, SYSTEM) starts a desktop helper in the
console session when a session begins and stops it when the session ends.

## Start a session

- **Remote control** in the menu lists the online Windows devices with a
  search box, the sessions that are running now and the session log.
- On a device page, **Remote control** opens the same viewer.

The viewer opens in its own browser tab. Its toolbar has:

- **Monitor**: one monitor at a time, or all of them together.
- **Quality**: low, medium or high (lower means smoother on slow links).
- **Ctrl+Alt+Del**, and a **Special keys** panel for Alt+Tab, Win, Win+R, Win+L,
  Ctrl+Esc, Ctrl+Shift+Esc, Alt+F4 and Print Screen.
- **Clipboard**: text copied on the remote machine shows up here; Ctrl+V in
  the viewer pastes your local clipboard on the remote machine; **Type it**
  types a text as keystrokes (for fields that block pasting, such as the
  sign-in screen).
- **Refresh the screen**, **Fit to window** and **Full screen** (in full
  screen, keys such as Alt+Tab and the Windows key go to the remote machine).
- **End session**.

The keyboard sends scan codes, so the remote machine's keyboard layout
applies, as on a physical keyboard. The sign-in screen, the lock screen and
UAC prompts work too: the helper follows the desktop that receives input.

Only users with the permission **Control devices remotely** (`remote.control`; the
Administrator and Technician roles have it) can start or end sessions.

## Terminal

Windows and Linux devices also have a **Terminal**: a shell in the browser,
without SSH or RDP. It opens in its own tab from the device page or from
**Remote control**.

- **Linux and Proxmox**: the login shell of root (from `/etc/passwd`, usually
  bash) on a pseudo terminal, with `TERM=xterm-256color` and the system
  locale. Full-screen programs such as `htop`, `nano`, `vim` and `mc` work.
- **Windows**: PowerShell, or Command Prompt (choose it in the toolbar), as
  SYSTEM on a ConPTY pseudo console.

The toolbar copies the selection (Ctrl+Shift+C; on Windows also Ctrl+C when
text is selected) and pastes (Ctrl+Shift+V; on Windows also Ctrl+V), and makes
the text smaller or larger. When the shell exits (`exit`), the output stays on
screen and **New session** starts another one.

A terminal has full control over the device, so it needs its own permission,
**Open a terminal on devices** (`remote.terminal`; Administrator and
Technician). It does not ask the signed-in user, whatever the group's consent
setting says: nothing of their screen is shown, as with SSH. Every terminal
session is in the session log and the audit log, like a screen session. Closing
the tab or **End session** hangs up the shell (SIGHUP on Linux, then SIGKILL
after 3 seconds; the shell and its console are ended on Windows).

A device runs at most 3 terminals and 2 screen sessions at the same time.

## Consent

Per device group, **Remote control** decides what the signed-in user sees:

| Setting | What happens |
| --- | --- |
| Automatic (servers direct, workstations ask), the default | Servers and hypervisors: direct. Workstations: ask. |
| Take over directly | The session starts without a message. |
| Notify the user | The session starts; the user sees a bar at the top of the screen with **End session**. |
| Ask the user | The user gets a pop-up with the operator's name and must click **Allow** within 30 seconds; otherwise the session is denied. While the session runs, the bar is shown. |

When nobody is signed in, a session starts directly (there is no one to ask).
When someone is signed in but the pop-up app cannot start, an *ask* session is
refused (`consent_unavailable`).

## Session log

Every session is logged with the device, operator, IP address, consent mode,
duration, transferred data and how it ended (for example *ended by the
operator*, *denied by the user*, *ended by the user*, *no answer*). Start and
end are also written to the audit log. The log is kept for 400 days.

## Security and network

- The browser talks to the server only, over HTTPS on port 443. The agent
  opens a separate, signed WebSocket for each session; the server relays it.
  Behind a reverse proxy, WebSocket upgrades must be forwarded (see
  [reverse-proxy.md](reverse-proxy.md)).
- The agent leg is encrypted end to end between agent and server with
  X25519 + AES-256-GCM on top of TLS, with a new key per session.
- The helper's named pipe only accepts the agent service (SYSTEM).
- An agent runs at most 6 sessions at the same time; a session has one viewer.

## Ctrl+Alt+Del

Windows only accepts a software Ctrl+Alt+Del from services when the policy
`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System\SoftwareSASGeneration`
allows it. The agent sets it to `1` (services) the first time it is needed, or
to `3` when it was `2` (ease of access apps). A Group Policy *Disable or enable
software Secure Attention Sequence* that says otherwise overrides this, so set
it to *Services* (or *Services and Ease of Access applications*).

## Direct connection

Once the screen shows, the viewer asks the agent for a direct connection: a
WebRTC data channel between the browser and the device. When the browser can
reach the device (the same network, a VPN, or through NAT with a STUN server),
the screen and the input switch to it and the toolbar shows **Direct**;
otherwise the session simply stays **Via server**. If the direct connection
drops, the session falls back to the server without interruption.

- The offer and answer go over the encrypted session socket, so the DTLS
  keys of the data channel are authenticated end to end; the data channel is
  encrypted with DTLS.
- **Settings > Remote control** turns it off (every session through the
  server) and takes optional STUN servers (`stun:host:port`). Without STUN
  servers only the device's own addresses are tried. No TURN server is used:
  where no direct route exists, the server relay is the fallback.
- The Windows installer adds two inbound firewall rules for UDP to
  `fleetwarden-agent.exe`, on domain and private networks only.

## Design notes

The plan named WebRTC with H.264. Both paths carry the same changed 64×64
tiles as JPEG instead: that works through every proxy and firewall that
allows HTTPS, needs no codec on the machines, and frame acknowledgements keep
latency low on slow links. The direct path cuts out the server's round trip
and its bandwidth.

## Troubleshooting

- *Taking over the screen is available for Windows devices*: Linux devices
  have a terminal instead.
- A terminal that says *The shell could not be started*: on Linux, check that
  root has a shell (`getent passwd root`); on Windows, that PowerShell is
  present (`%SystemRoot%\System32\WindowsPowerShell\v1.0`). The agent log has
  the error.
- On the machine, `fleetwarden-agent remote-test --screenshot C:\shot.png`
  (as SYSTEM, for example with `psexec -s`) starts the helper like a session
  and saves the first frame.
