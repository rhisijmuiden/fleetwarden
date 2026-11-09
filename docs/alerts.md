# Alerts

*Nederlands: [alerts.nl.md](alerts.nl.md)*

**Alerts** in the menu shows the devices that need attention now. The red
number next to it is the number of open alerts nobody has acknowledged yet;
the dashboard shows it too. An alert opens when a rule's condition is met and
closes by itself once the problem is gone.

## Rules

There are three kinds of rules:

- **A device is offline**: the agent hasn't been in touch for the chosen
  time (at least 5 minutes; agents check in every minute).
- **A disk is almost full**: a disk has less free space than the chosen
  percentage. Disks smaller than 2 GB (EFI and recovery partitions) are left
  out. An alert stays open until the disk has at least one percentage point
  more free space than the limit, so it doesn't open and close all the time.
- **The warranty ends**: the warranty date of the device (from
  [Assets](assets.md)) is at most the chosen number of days away (0 to 365),
  or has passed. The alert closes when a later date is filled in.

Each rule can be limited to device groups and to workstations, servers or
hypervisors, and mails up to 20 addresses in English or Dutch, also when the
alert is resolved if you want. Without recipients the alerts only show in the
web interface. New installations start with two rules without recipients:
*Server offline* (servers and hypervisors, 15 minutes) and *Low disk space*
(10%), plus *Warranty ending* (60 days) since asset management. The rules are checked every minute; one e-mail lists all alerts a rule
opened at the same time.

Agents send their free disk space with every heartbeat, so a disk alert
opens within a few minutes. Agents older than this version only report it
with their inventory. After a server restart, offline alerts wait for the
chosen time so the agents can reconnect first.

## Permissions

Everyone who can view devices sees the alerts of their groups.
**Acknowledge** (Administrators and Technicians, permission *Acknowledge
alerts and manage alert rules*) marks an alert as seen: it stays in the list
but no longer counts in the red number. The rules can only be changed by
users with that permission who see every group, because a rule looks at the
whole fleet.

## API

| Method | Path | Permission |
| --- | --- | --- |
| GET | `/api/v1/alerts?state=all&device=` | devices.view |
| GET | `/api/v1/alerts/count` | devices.view |
| POST | `/api/v1/alerts/{id}/acknowledge` | alerts.manage |
| GET, POST | `/api/v1/alerts/rules` | alerts.manage, all groups |
| PUT, DELETE | `/api/v1/alerts/rules/{id}` | alerts.manage, all groups |
