# Management without taking over the screen

*Nederlands: [manage.nl.md](manage.nl.md)*

The **Manage** tab on a device page handles a Windows or Linux device while
its user keeps working: nothing here shows on their screen or asks them
anything. The agent does the work as SYSTEM (Windows) or root (Linux).

The tab needs the permission **Manage devices without taking over**
(`remote.manage`). The Administrator and Technician roles have it; read-only
users do not see the tab. Group scoping applies as everywhere else: a user
who only sees some groups can only manage those devices. The device has to be
online.

Every change (ending a process, a service action, creating, renaming,
deleting, downloading or uploading a file) is written to the audit log with
the device, the path or name and the user who did it.

## Processes

The running programs with their PID, user (with the Windows session), CPU
use measured over half a second, memory (working set / RSS), start time and
command line. Sort by any column, search by name, PID, user or command line,
and switch on **Refresh every 5 s** to watch a program.

**End** stops a process (Windows: `TerminateProcess`; Linux: `SIGTERM`, and
`SIGKILL` after five seconds). Processes Windows cannot run without (System,
smss, csrss, wininit, winlogon, services, lsass), init and kernel threads on
Linux, and the agent itself are protected and cannot be ended.

## Services

- **Windows**: all services with their state, startup type and account.
  Start, stop and restart a service and change its startup type (automatic,
  manual, disabled). Stopping a service that others depend on fails with a
  clear message; stop those first.
- **Linux**: all systemd services. Start, stop and restart; **Automatic** is
  `systemctl enable`, **Manual** is `systemctl disable`, **Disabled** is
  `systemctl mask`. Static units have no startup type to change.

The agent's own service is shown but cannot be changed from here. Stopping,
restarting or disabling asks for confirmation first.

## Event logs

- **Windows**: Application, System, Security and Setup, plus the PowerShell,
  Windows Defender, Task Scheduler, Windows Update, Remote Desktop session and
  BITS operational logs when the machine has them. Messages are formatted by
  the event's own provider, in the device's language.
- **Linux**: the systemd journal (everything, or only kernel messages).

Filter by level (critical, errors, warnings, information or everything),
period, source (Windows provider, or a systemd unit or program on Linux),
event ID and text. The newest events come first, up to 2,000 per search;
click an event for the full text. **Export CSV** saves the list.

## Files

Browse the drives (Windows) or the file system from `/` (Linux), or type a
path. Folders can be created, files and folders renamed and deleted (a folder
with everything in it, after confirmation). Drive roots cannot be deleted.

- **Download** a file, or a whole folder as a zip file (files that are in use
  or not readable are skipped). Large files are fine: the agent sends them to
  the server in blocks of 8 MB, so they also pass through the secure gateway.
- **Upload** one or more files to the open folder with the button or by
  dropping them on the list. Existing files are only replaced after you
  confirm. The agent checks the SHA-256 before it puts the file in place.

The limit is 4 GB per file. Transfers run in the background and are listed
under **Transfers** with their progress; a finished download stays available
for an hour (only to the user who started it) and is then deleted from the
server. Transfers are kept in `DataDir/transfers`, which is not backed up,
and do not survive a restart of the server.

## Console

**Console** in the menu opens the PowerShell or Command Prompt (Windows) or root shell (Linux) of
[remote control](remote.md) in its own browser tab, also
without taking over the screen.

## How it works

The server sends a signed request over the agent's existing connection and
waits for the signed answer; nothing is stored on the server except the audit
log. File contents do not go over that connection: the agent uploads or
downloads them with requests signed by its device key, and only for a
transfer the server opened for that device.
