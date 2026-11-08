# Patch management

*Nederlands: [patching.nl.md](patching.nl.md)*

FleetWarden installs Windows updates and apt updates (Debian, Ubuntu, Proxmox
VE) in maintenance windows you set per device group. Open **Patch management**
in the menu.

## How it works

1. Create a **patch policy** under **Patch management > Policies** and assign
   it to one or more groups under **Groups** (edit a group, *Patch policy*).
   Devices in a group without a policy are not patched automatically.
2. Online agents scan for missing updates at the interval under **Settings >
   Agent scans** (every 6 hours by default; a group can have its own) and
   right after a job. **Scan now** on a device's **Updates** tab
   scans at once.
3. When the window opens, the server sends each online device one job with its
   approved updates. Devices that are offline get their updates in the next
   window. A window may run past midnight; it uses the policy's time zone (or
   the server's).
4. The agent asks the logged-on user first when the policy says so, installs,
   and restarts according to the restart setting. The job and its result per
   update show in the device's patch history and under **Patch management >
   Overview**.

## Approval

Each update falls into a category: security, critical, cumulative,
definitions, drivers, feature updates or other. On Linux an update is
*security* when it comes from a security suite (for example
`trixie-security`); everything else is *other*.

Per category the policy says:

- **Approve automatically**, optionally only after a number of days (counted
  from the release date, or from when FleetWarden first saw the update). The
  default waits 7 days for cumulative updates and 3 days for other updates.
- **Approve by hand**: only installed after you approve it.
- **Never install** (the default for feature updates such as a new Windows
  version).

Under **Patch management > Updates** you see every missing update across your
devices. **Approve** or **Decline** applies to every device and always wins
over the category rule; **Follow policy** removes the decision. On a device's
**Updates** tab you can also select updates and install them right away
(outside the window), for example a driver for one machine.

## Restart and the user

- **Ask the user, then restart**: the user may postpone the restart (up to
  *Times the user may postpone*, each time by *Postpone by* minutes); after
  the last postpone the device restarts anyway.
- **Restart automatically**: restarts right after installing; a logged-on
  Windows user sees the pop-up with a countdown first.
- **Never restart**: the device reports *Restart pending* and you restart it
  yourself.

With **Ask the logged-on user first**, Windows shows a pop-up at the bottom
right before installing, with the same postpone rules. A restart always
shows the pop-up to a logged-on user, also when this is off. Without an
answer within the countdown the job goes ahead. Without a logged-on user
(servers) the job starts right away. The pop-up uses your branding (product
name and colour) and the user's Windows language (English or Dutch).

The pop-up comes from the FleetWarden tray app (`FleetWardenTray.exe`),
which the MSI installs next to the agent. It starts at logon, and the agent
also starts it in open sessions when it has something to ask. It needs no
extra runtime (.NET Framework 4.7.2 is part of Windows 10 and 11) and talks
to the agent service over a local named pipe only.

## Windows Update

With **Take over Windows Update** on (the default), the agent turns off
Windows' own automatic installs, so updates only arrive in your windows.
Turning it off, or removing the policy, gives control back to Windows.

If devices also get updates from Intune (Windows Update for Business) or a
WSUS server set by GPO, choose one tool to be in charge: two tools that both
install updates will get in each other's way. FleetWarden uses the update
source Windows is configured for (Microsoft Update or WSUS) and shows it on
the device.

## Debian, Ubuntu and Proxmox VE

The agent runs `apt-get update` and simulates `apt-get dist-upgrade` to find
missing updates, so the list matches what Proxmox recommends (never a plain
`apt upgrade`). A job upgrades exactly the approved packages; apt pulls in new
dependencies. FleetWarden's own packages are left to the agent and server
updaters. A restart is pending when `/run/reboot-required` exists or a newer
kernel than the running one is installed.

**Proxmox VE clusters** are patched one node at a time: the next node of the
same cluster only starts when the previous node's job (including its restart)
is done. Give Proxmox nodes their own group and policy, for example with a
night window and *Restart automatically*, and make sure HA or a migration plan
covers the guests during the restart.

## Permissions

- **Manage patch policies**: administrators.
- **Approve or decline updates**: administrators and technicians.
- **Scan now / Install now** on a device: anyone who may manage devices.
