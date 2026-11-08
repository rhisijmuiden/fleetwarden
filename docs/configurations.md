# Configuration management

*Nederlands: [configurations.nl.md](configurations.nl.md)*

FleetWarden sets and keeps Windows and Linux settings on devices, also on PCs
that are not in a domain. The agent applies them, checks them again every 30
minutes, puts back what someone changed, and removes what it set when a
setting no longer applies. Open **Configurations** in the menu.

## Configurations and profiles

A **configuration** is one reusable item: a browser policy, a set of network
drives, a firewall rule set, and so on. A **profile** bundles configurations
and is linked to device groups. A device gets every enabled configuration of
the profiles of its groups. When a device joins a group, it gets the
profile's settings at once; when it leaves the group, they are removed.

1. **Configurations > New configuration**: pick the platform and type, fill
   it in and save.
2. **Configurations > Profiles > New profile**: pick the configurations and
   the device groups.

When two configurations set the same thing, the one with the higher *order*
number wins. A disabled configuration is removed from the devices.

## Types

| Type | Platform | What it does |
|------|----------|--------------|
| Browser policy | Windows, Linux | Start page, home button, password manager, forced extensions for Chrome, Edge and Firefox |
| Registry | Windows | Values in HKLM, or in HKCU of every user who logs on |
| Script | Windows, Linux | Runs once, at startup, at logon or on an interval, as SYSTEM/root or as the user |
| Network drives | Windows | Maps drives such as `H:` → `\\server\home\%USERNAME%` for every user |
| Printers | Windows | Shared printers and TCP/IP printers with their driver, and the default printer |
| Desktop | Windows | Wallpaper of every user, and locking the PC after minutes without input |
| Power | Windows | Screen off, sleep and hibernate, on mains power and on battery |
| Local administrators | Windows | Adds accounts and groups to the local Administrators group |
| Firewall | Windows | Windows Firewall rules by direction, protocol, ports, addresses and program |

Notes per type:

- **Network drives** use the user's own Windows sign-in. On a PC without a
  domain Windows asks for a password the first time, unless the user has the
  same account on the server.
- **Printers**: TCP/IP printers need a driver name that is in Windows, or a
  `.inf` file on a share. Shared printers can be added for every user of the
  device or per user.
- **Desktop** and **Power** are written as Windows policies, the same keys
  group policy uses: users cannot change them. A new wallpaper shows at the
  next sign-in; power settings apply at once.
- **Local administrators**: members FleetWarden added are removed again when
  they are no longer configured. Other members stay. The group is found by
  its SID (S-1-5-32-544), so it works in every Windows language.
- **Firewall** rules are in the Windows Firewall group *FleetWarden*.

## Status and Group Policy

The configuration page shows per device whether the settings are applied,
waiting (for example for a user to log on), failed or unsupported, and how
often the agent repaired one. **Apply now** on a device applies everything at
once. When a device is in a domain, FleetWarden warns that Group Policy may
set the same things; Group Policy wins where both write the same policy.
