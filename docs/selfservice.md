# Self-service

Users of Windows devices can help themselves from the FleetWarden icon in the
system tray: right-click it and choose **Software and help**. The window has
three parts, each of which you can turn on or off. Dutch version:
[selfservice.nl.md](selfservice.nl.md).

| Part | What the user can do |
|---|---|
| **Software** | Install programs you published in the catalog, with or without approval. |
| **Updates** | Install the updates their group's patch policy approved now, instead of waiting for the maintenance window. |
| **Help** | Send a question to IT; it arrives with the user's name and computer. |

The agent knows which user asks, from the Windows session that runs the tray
app, so requests show who sent them.

## Catalog

Under **Self-service > Catalog** choose **Add program** and pick a package from
the software library (*Software*). Per program you set:

- a name, category and description for users (empty: from the package);
- **Visible to**: device groups, or none for every group;
- **Needs approval**: a technician approves each request first, for example
  for programs with a paid licence. Without approval the install starts at
  once;
- **Show in the catalog**: hide a program without removing it.

Installs run as normal deployments, so they show up under *Deployments* with
"By the user (self-service)" as the source.

## Requests and help

- **Install requests** lists the requests that wait for approval. Approving
  installs the program right away, or as soon as the device is online.
  Rejecting shows your note to the user.
- **Help requests** are the questions from users. Take one, reply (the reply
  appears in the user's window), open remote control on the device and close
  it when done.

## Settings

Under **Self-service > Settings** you turn the three parts on or off for all
devices, set a text above the help form (for example opening hours or the
service desk number), and enter up to 20 e-mail addresses that get a mail for
each help request and each request that needs approval.

## Permissions

| Permission | Administrator | Technician |
|---|---|---|
| Manage the catalog and settings (`selfservice.manage`) | yes | no |
| Approve install requests and handle help requests (`selfservice.handle`) | yes | yes |

## Limits

- Self-service is only in the Windows tray app; Linux devices have no tray
  window.
