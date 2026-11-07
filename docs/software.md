# Software deployment

*Nederlands: [software.nl.md](software.nl.md)*

FleetWarden installs and removes software on Windows and Linux devices. Open
**Software** in the menu for the library and the installed software, and
**Deployments** for the jobs and their results.

## The software library

**Software > Library > Add package** offers three sources:

- **winget catalog**: search the public winget catalog (the same catalog
  `winget` uses, over 15,000 applications). FleetWarden picks the best
  installer for each device (machine-wide, x64 or arm64, MSI before EXE) with
  the silent arguments from the catalog. Choose *Latest* to always install the
  newest version, or pin a version.
- **Upload installer**: your own `.msi` or `.exe` for Windows, or a `.deb` for
  Linux, up to 2 GB. FleetWarden recognises Inno Setup and NSIS installers and
  fills in their silent arguments; for other EXE installers enter the vendor's
  silent arguments (for example `/S`, `/quiet` or `/silent`).
- **apt package**: a package name such as `htop`, installed from the
  repositories the Linux device already uses.

Under *Advanced* you can set the name used to detect the program in
*Programs and Features*, an MSI product code and a timeout.

### The winget catalog

The server downloads the catalog index (about 20 MB) from
`cdn.winget.microsoft.com` once a day and keeps it in
`/var/lib/fleetwarden/catalog`. **Refresh now** on the Software page loads it
at once. The devices themselves don't need winget: the agent downloads the
installer straight from the vendor's HTTPS address and checks its SHA-256
hash against the catalog before it runs it. Some vendors (Google Chrome, for
example) replace the file behind the same link; for those packages turn on
*Allow a changed installer* under *Advanced*.

Uploaded installers are stored on the server (`/var/lib/fleetwarden/software`)
and only devices with an open job for that file can download them. They are
part of the backup (full and incremental), so a restore brings them back;
the winget catalog is not, it is downloaded again.

## Deploying

Choose **Deploy** next to a package and pick groups, devices or both:

- **Keep the groups up to date**: devices that join the groups later get the
  job as well, until you cancel the deployment.
- **Start later**: the devices start from the chosen time.
- **Expires after**: devices that haven't started (for example because they
  were offline) are marked *expired* after this many days.

Each device runs one software job at a time, and never at the same time as a
patch install. The agent first checks whether the program is already
installed (with the same or a newer version); then the job is *skipped*.
Otherwise it downloads, installs silently as SYSTEM or root, and reports the
result: exit code, the end of the installer log, the version it finds
afterwards and whether a restart is needed (exit codes 3010 and 1641). A job
that needs a restart doesn't restart the device; the next patch window or you
do that.

**Deployments** shows every deployment with its progress. Open one to see the
result per device, the installer log, and to **Retry** failed devices or
**Cancel** the rest. A device's **Software** tab shows its own jobs and has
**Install software** for one device.

## Uninstalling

You can remove any program from the inventory, also programs FleetWarden
didn't install:

- **Software > Installed software** lists every program across your devices.
  **Uninstall** selects every device that has it; deselect devices or add
  groups as you like.
- On a device's **Software** tab, the bin icon removes a program from that
  device.

On Windows the agent uses the program's quiet uninstaller when it has one,
`msiexec /x {product code} /qn /norestart` for MSI packages, and the standard
silent switches for Inno Setup and NSIS uninstallers. For other programs add
the vendor's silent arguments under *Uninstall arguments*. On Linux it runs
`apt-get remove` (or `purge` with *Remove configuration files too*). The
FleetWarden agent itself can't be removed this way.

## Permissions

- **Manage the software library** (`software.manage`, administrators): add,
  change and delete packages, upload installers, refresh the catalog.
- **Deploy and uninstall software** (`software.deploy`, administrators and
  technicians): start, retry and cancel deployments.
- Read-only users see the library, the installed software and the
  deployments. Users limited to device groups only see and target their own
  groups.

## Reverse proxy

Uploads can be up to 2 GB. Set the upload limit of your reverse proxy
accordingly, for nginx `client_max_body_size 2g;` (see
[reverse-proxy.md](reverse-proxy.md)).
