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
- An agent runs at most 4 sessions at the same time; a session has one viewer.

## Ctrl+Alt+Del

Windows only accepts a software Ctrl+Alt+Del from services when the policy
`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System\SoftwareSASGeneration`
allows it. The agent sets it to `1` (services) the first time it is needed, or
to `3` when it was `2` (ease of access apps). A Group Policy *Disable or enable
software Secure Attention Sequence* that says otherwise overrides this, so set
it to *Services* (or *Services and Ease of Access applications*).

## Design notes

The plan named WebRTC with H.264. This version sends changed 64×64 tiles as
JPEG over the WebSocket relay instead: it works through every proxy and
firewall that allows HTTPS, needs no codec on the machines and no TURN
server, and frame acknowledgements keep latency low on slow links. A direct
WebRTC path and a web terminal for Linux follow in the next step.

## Troubleshooting

- *Remote control is only available for Windows devices*: Linux support (a
  terminal) comes later.
- On the machine, `fleetwarden-agent remote-test --screenshot C:\shot.png`
  (as SYSTEM, for example with `psexec -s`) starts the helper like a session
  and saves the first frame.
