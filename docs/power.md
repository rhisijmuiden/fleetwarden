# Power management

*Nederlands: [power.nl.md](power.nl.md)*

**Power** in the menu wakes switched-off devices with Wake-on-LAN, and
restarts and shuts down devices by hand or on a weekly schedule. A patch
policy can also wake the devices that are off when its window opens and shut
them down again once their updates are installed.

## Wake-on-LAN

A wake-up packet ("magic packet") is a broadcast in the local network: it
does not cross routers and cannot be sent over the internet. FleetWarden
therefore asks up to three **online agents in the same network** to send it.
It picks agents behind the same public address first (two sites may use the
same private range), then servers (they are always on), then workstations.
The server sends the packet as well when **Send from the server too** is on,
which helps when the server is in that network.

The agent reports each network adapter with its MAC address and network
(for example `10.20.0.41/24`). Agents older than this version only report
their addresses; FleetWarden then assumes a /24 network.

A wake has worked when the device comes online within 15 minutes; otherwise
the history shows it as failed.

**Power > Devices** shows for every switched-off device whether it can be
woken and through which devices. Select devices there to wake, restart or
shut down several at once.

### What Wake-on-LAN needs

- Wake-on-LAN (or "Power on by PCI-E", "Remote wake up") is on in the BIOS or
  UEFI. FleetWarden cannot switch that on.
- Another device with the agent is online in the same network (VLAN), or the
  server is in that network.
- The device is connected with a network cable. Wi-Fi adapters rarely wake a
  computer that is shut down.

**Power > Settings > Prepare devices for Wake-on-LAN** has the agents do the
rest:

- Windows: "wake on magic packet" is turned on for the wired physical
  adapters, and Fast Startup is turned off. A computer shut down with Fast
  Startup is half hibernated and many network adapters do not wake it.
  Turning the setting off puts Fast Startup back as it was.
- Linux: `ethtool -s <adapter> wol g` for the wired adapters that support it
  (`ethtool` must be installed). The agent applies it again each time it
  starts.

The UDP port is 9 by default; some network cards only listen on 7.

## Restart and shut down

On a device page, **Restart** and **Shut down** (online devices) and **Wake**
(switched-off devices) carry out an action right away. Options:

- **Warn the logged-on user**: the tray pop-up shows a countdown; the user can
  restart or shut down now, or postpone the set number of times.
- **Only when nobody is logged on**: the device is skipped while someone works
  on it.

Windows uses `shutdown /r` or `/s` (a full shutdown, not a hybrid one);
Linux uses `systemctl reboot` or `poweroff`.

Permissions: wake, restart and shut down need **power.control**
(administrators and technicians); schedules and settings need
**power.manage** (administrators).

## Schedules

**Power > Schedules** wakes, restarts or shuts down the devices of one or more
groups on set days at a set time, in the server's or a chosen time zone. For
example:

- *Workstations off in the evening*: shut down, Monday to Friday 19:00, warn
  the user with 2 postpones of 30 minutes.
- *Workstations on in the morning*: wake, Monday to Friday 07:30.
- *Restart servers after updates*: restart, Sunday 03:00, only when updates
  wait for a restart.

A wake schedule only wakes devices that are off; a restart or shutdown
schedule only reaches devices that are online at that moment. A slot that the
server missed by more than 30 minutes (because it was down) is skipped.
**Run now** carries out a schedule right away.

## Patch windows

A patch policy has two power switches:

- **Wake devices for the window**: when the window opens, the devices of its
  groups that are off are woken (a second attempt follows when the first did
  not work, as long as at least half an hour of the window is left).
- **Shut down woken devices afterwards**: a device that was woken for the
  window is shut down again when its patch job has finished (after the
  restart, if there was one), or after an hour online without a job, or when
  the window ends. Never while someone is logged on, and never a device that
  was already on when the window opened.

## Power plans

Sleep, hibernate and screen timeouts are part of configuration management:
create a **Windows settings > Power** configuration and add it to a
configuration profile. See [configurations.md](configurations.md).

## History

**Power > History** lists every wake, restart and shutdown of the last 400
days: who or what started it (by hand, a schedule, the patch window, after
patching), which devices sent the wake-up packet, the user's postpones and
the outcome.
