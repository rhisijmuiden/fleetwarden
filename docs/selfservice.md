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

## Tickets from the tray

When help requests are on, the help form in the tray becomes a ticket form.
Under **Self-service > Settings** you choose:

- **Where a ticket goes**: FleetWarden only (Help requests and an e-mail to
  the recipients), an e-mail in the user's own **Outlook** (opened ready to
  send, with the device report as an HTML attachment; without classic Outlook
  the default mail program opens and Explorer shows the report to attach), an
  e-mail through FleetWarden's **mail server**, or a **service desk
  integration** (see [integrations](integrations.md)). With an integration you
  can fall back to e-mail when it fails after all attempts. Every ticket also
  stays under Help requests, with the service desk's ticket number, link and
  state (checked every 5 minutes; a ticket closed in the service desk closes
  the help request). A reply you write under Help requests is added to the
  service desk ticket as a private note.
- **The form**: a required description with a minimum length, urgency (low,
  normal, high; the integration's priority), a category from your list, how to
  reach the user, and whether the user sees what is sent along.
- **Device information**: logged-on user (with the e-mail address when
  Windows knows it), time, IP addresses, OS/domain/group, model/serial/CPU/RAM,
  free disk space, uptime, reboot pending, waiting updates, catalog and
  third-party software (deployments and outdated programs, not built-in
  Windows parts), security status and open alerts. The computer name is
  always sent.
- **Link to FleetWarden**: a link to the device page, plus quick actions
  (remote control, terminal, scan). It needs the public URL under Settings >
  General. The link holds no token: the technician signs in (SSO or local),
  lands on the device with the help request at the top, and starts the action
  with one click. Permissions and the group's consent rules apply as always.

**Preview ticket** shows the ticket a real device would send with the
settings in the form, before you save them.

## Chat with the user

On a Windows device, **Chat** (device page, or **Chat with the user** in a
help request) opens a chat panel at the bottom right. The first message starts
the chat: it opens as a window at the bottom right of the signed-on user's
screen, in the tray app. The user can only answer an open chat, never start
one. The panel shows whether a tray shows the chat. With fast user switching
every signed-on user sees it.

**End chat** closes it for both sides; a chat without messages for an hour
ends by itself. A chat opened from a help request (or from the ticket link
action *Chat*) is linked to it: when it ends, the transcript is added to the
service desk ticket as a private note. Starting and ending a chat is in the
audit log. Limits: 2,000 characters per message and 1,000 messages per chat.
It needs `selfservice.handle`.

## Permissions

| Permission | Administrator | Technician |
|---|---|---|
| Manage the catalog and settings (`selfservice.manage`) | yes | no |
| Approve install requests and handle help requests (`selfservice.handle`) | yes | yes |

## Limits

- Self-service is only in the Windows tray app; Linux devices have no tray
  window.
