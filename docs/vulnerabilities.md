# Vulnerabilities (CVEs)

*Nederlands: [vulnerabilities.nl.md](vulnerabilities.nl.md)*

FleetWarden links the installed software, the Windows build and the missing
patches of every device to known vulnerabilities (CVEs). For each
vulnerability you see its severity, whether it is being exploited in the
wild and how to fix it, and you can roll out the fix from the same screen.
Open **Vulnerabilities** in the menu.

## Where the data comes from

The server reads four public sources once a day. Devices never contact them.

| Source | Used for | Address |
| --- | --- | --- |
| NVD (NIST) | descriptions, CVSS score and the affected versions of Windows programs | `nvd.nist.gov/feeds/json/cve/2.0/` |
| CISA KEV | which CVEs are known to be exploited (**Exploited** badge) | `www.cisa.gov/.../known_exploited_vulnerabilities.json` |
| Microsoft MSRC | Windows CVEs, the fixed build per Windows version and the KB that fixes them | `api.msrc.microsoft.com/cvrf/v3.0/` |
| Debian security tracker | CVEs per Debian package and the version that fixes them (Debian and Proxmox VE) | `security-tracker.debian.org/tracker/data/json` |

The first time, the server reads all NVD years (about 400 MB of compressed
files, a few minutes). After that it only reads the NVD feed of recent
changes, and the full set again once a week. Microsoft is read for the last
12 months by default (3 to 36 under **Sources & settings**); older months are only read
again when Microsoft changes them. A source that fails is tried again after
two hours; the error is shown under **Sources & settings**.

The server must be able to reach these addresses over HTTPS. Behind a proxy
or firewall, allow them.

Under **Vulnerabilities > Sources & settings** you can switch vulnerability scanning or
single sources off, change the number of Microsoft months and read the
sources again right away with **Update now**.

## How devices are matched

Matching runs on the server, from what the agents already report; nothing
extra runs on the devices. A device is checked again after every inventory
and update scan, and all devices after every source update.

- **Windows itself**: the Windows version and build (for example Windows 11
  24H2, build 26100.2314) are compared with the fixed build Microsoft lists
  per CVE. The fix is the missing cumulative update that contains it.
- **Missing Windows updates**: every missing update whose KB Microsoft lists
  as a fix for a CVE (for example a .NET or Office update) is linked to those
  CVEs.
- **Windows programs**: a list of widely used programs is compared with the
  affected version ranges in NVD: Chrome, Edge, Firefox, Firefox ESR,
  Thunderbird, 7-Zip, VLC, Notepad++, Adobe Acrobat (Reader), PuTTY, WinSCP,
  Wireshark, Git, FileZilla, KeePass, TeamViewer, AnyDesk, Visual Studio
  Code, LibreOffice, Foxit PDF Reader, Node.js, VirtualBox, VMware
  Workstation, WinRAR, Slack, Docker Desktop, PowerShell, Zoom, Greenshot and
  Python. The full list is under **Sources & settings**. Other programs are not matched:
  NVD names products differently from how Windows lists them, and guessing
  gives false alarms.
- **Debian and Proxmox VE** (bullseye, bookworm, trixie): every installed
  package is compared with the Debian security tracker by its source package
  and version. CVEs Debian marks as unimportant, or for which no fix is known
  yet, are left out. The agent reports the source package from this version
  on; devices with an older agent are matched once they have updated.

Ubuntu, other Linux distributions and macOS are not matched yet; the device
tab says so.

## Severity and fix

The severity comes from the CVSS score (NVD, or Microsoft for Windows CVEs):
**Critical** (9.0 and up), **High** (7.0), **Medium** (4.0), **Low** and
**Unrated**. CVEs in the CISA list get the **Exploited** badge; handle those
first.

Each finding has a fix:

| Fix | Meaning | Roll out with |
| --- | --- | --- |
| **Update available** | a missing Windows or Debian update the device is offered fixes it | a patch job on the device |
| **New version available** | a newer version of the program fixes it | a software deployment via winget (or the browser update) |
| **Fixed, not offered yet** | a fixed version exists, but the device is not offered it yet (for example a Windows version that no longer gets updates, or a Debian fix not yet in the device's repositories) | – |
| **No fix yet** | no fix known yet | – |

## Rolling out a fix

Select CVEs in the list, on the CVE page or on the **Vulnerabilities** tab of
a device, and choose **Roll out fix**. You need the **Manage devices**
permission.

- Patches are installed right away on the online devices with a manual
  patch job, with the device's patch policy (restart settings, maintenance
  window and update rings do not apply to a manual job). Devices with a patch
  job still running and Proxmox cluster nodes while another node is patching
  are skipped, and so are offline devices: they get the update in their next
  maintenance window as usual. The result lists them.
- Programs are updated with a software deployment to the affected devices.
  The winget package is added to the software library first when it is not
  there yet (named *Vulnerability fix: ...*). Browsers use the browser update
  action. Devices pick the deployment up at their next check-in, also when
  they were offline.

The result shows per device what was started and what was skipped, with the
reason. Every fix is written to the audit log.

## Accepting a risk

When a vulnerability does not apply or cannot be fixed yet, accept the risk
with the shield button: for one device or for all devices, with a reason and
an optional end date. Accepted CVEs no longer count in the totals, the
dashboard and the device list; **Show accepted risks** shows them again. Accepting
needs the **Manage vulnerability sources and accepted risks** permission (administrators), and for all
devices access to all devices. Accepted risks are under
**Vulnerabilities > Accepted risks** and are removed automatically when they
expire.

## Backup and restore

The vulnerability sources are not part of the backup: they are public and
large. After a restore the server reads them again; devices are matched
again once that is done. Findings, accepted risks and the settings are in
the backup.
