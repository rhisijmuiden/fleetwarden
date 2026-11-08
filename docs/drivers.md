# Drivers and firmware

*Nederlands: [drivers.nl.md](drivers.nl.md)*

FleetWarden keeps its own repository of driver and firmware updates. Devices
report which updates they can get, but nothing is downloaded until an
administrator adds an update to the repository. The server then downloads it
once from the vendor and devices install it from the server, never from the
internet. Open **Drivers & firmware** in the menu.

## Where updates come from

- **Windows**: the driver and firmware updates Windows Update offers for the
  hardware (graphics, network, chipset, Surface and other UEFI firmware). The
  agent asks Windows Update during each update scan, but does not download.
- **Linux** (Debian, Ubuntu, Proxmox VE): firmware from the Linux Vendor
  Firmware Service through `fwupd`, when it is installed
  (`apt install fwupd`). fwupd only fetches its list of updates; the firmware
  itself comes from the repository.

- **BIOS from Dell, HP and Lenovo**: the server reads the vendors' BIOS
  catalogs for the models among your Windows devices (see below).
- **Your own packages**: drivers and firmware you upload yourself (see below).

## BIOS from the vendor catalogs

Once a day, and right away for a model it has not seen, the server reads the
BIOS catalog of each Dell, HP and Lenovo model among the Windows devices.
Only the catalog files are read; the newest BIOS a device can take is shown
under **Available** like any other update, and only downloaded once you add
it to the repository. The download is checked against the vendor's SHA-256.

| Vendor | Matched on | Catalog | Installed with |
| --- | --- | --- | --- |
| Dell | System ID (SKU) and BIOS version | `downloads.dell.com/catalog/CatalogIndexPC.cab` | the Dell Update Package, `/s` |
| HP | Platform ID (baseboard), ROM family and version | HP Image Assistant reference file (`hpia.hpcloud.hp.com`) | `HpFirmwareUpdRec.exe -b -s` from the SoftPaq |
| Lenovo | Machine type and BIOS level | `download.lenovo.com/catalog/<type>_Win11.xml` | `winuptp.exe -s` from the package |

The agent reports the system ID, baseboard and bare BIOS version with its
inventory; devices with an older agent are matched once they have updated.
The server needs `cabextract` to unpack the Dell and HP catalogs (a
dependency of the package). Under **Drivers & firmware > Vendor catalogs**
you can switch each vendor off, read the catalogs again right away and see
which models were found. Vendor BIOS updates are firmware: they are only
installed in the patch window when the policy allows firmware, BitLocker is
suspended for one restart and the vendor tool does not restart by itself;
the patch job restarts with the policy's restart settings. A BIOS with a
setup password cannot be updated silently by Dell's tool; the job then
reports that.

Vendor drivers from these catalogs are not offered: Windows Update has the
same drivers for these models, and matching them needs the PCI IDs of every
device. Upload a vendor driver yourself when you need a specific one.

## Your own packages

Under **Drivers & firmware > Own packages**, **Upload package** stores a
package on the server right away (at most 2 GB; behind a reverse proxy allow
uploads of that size, see [reverse-proxy.md](reverse-proxy.md)):

- **A driver for Windows**: a `.zip` or `.cab` with the driver's `.inf`
  (and its `.sys` and `.cat`). The agent unpacks it and installs every
  `.inf` with `pnputil /add-driver /subdirs /install`.
- **A program for Windows** (`.exe`), such as a vendor's driver or BIOS
  installer, with the arguments for a silent install (for example `/s`).
  It runs as SYSTEM; exit codes 0, 3010 and 1641 mean success.
- **A firmware cabinet for Linux** (`.cab` for fwupd), installed with
  `fwupdmgr install`.

Choose the devices with **Manufacturer contains** and **Model contains**
(compared with the inventory, ignoring case; empty matches every device of
the platform). The package is offered to those devices and installed like a
repository update: in the patch window (drivers or firmware, per policy) or
with **Install now** on a device. Uploaded packages are part of the backup,
because they cannot be downloaded again; they are kept under
`/var/lib/fleetwarden/driver-uploads`.

## How it works

1. Devices in a group with a patch policy scan at the interval under
   **Settings > Agent scans**. Each scan also lists driver and firmware
   updates. They show under **Drivers & firmware > Available**, with the
   number of devices that can install them; **Scan now** on a device scans
   at once.
2. Select updates and choose **Add to repository**. The server downloads the
   files from the vendor (only from the download servers of Microsoft, LVFS,
   Dell, HP and Lenovo),
   checks the vendor's checksum when there is one, and stores them under
   `/var/lib/fleetwarden/repository`. A failed download is tried three
   times; **Try again** starts over. **Reject** hides an update you do not
   want.
3. In the patch window, devices whose policy has **Install drivers from the
   repository** (on by default) or **Install firmware (BIOS/UEFI) from the
   repository** (off by default) install the repository updates they need,
   in the same job as the other updates and with the same pop-up and restart
   settings. On a device's **Drivers** tab you can also install repository
   updates right away.
4. **Remove from repository** deletes the files; the update goes back to the
   available list.

## On the device

- **Windows**: the agent fetches the cabinet from the server, unpacks it and
  installs the driver with `pnputil /add-driver /install`, which updates the
  matching devices. With **Install drivers from the repository** on, Windows
  no longer fetches drivers by itself when a device is plugged in (the agent
  sets *Device installation settings* to *No*, and restores it when the
  setting is turned off). Use **Take over Windows Update** so Windows Update
  does not install drivers on its own either.
- **Firmware on Windows** is staged and applied by Windows during the next
  restart. The agent suspends BitLocker for that one restart, so the device
  does not ask for the recovery key.
- **Dell, HP and Lenovo BIOS**: the agent runs the vendor's tool silently
  (see the table above), after extracting the SoftPaq or Lenovo package.
- **Linux**: the agent runs `fwupdmgr install` with the cabinet from the
  server on the device it was offered for. UEFI firmware is applied during the
  next restart.

Firmware updates can make a device unusable when they go wrong (power loss
during the update). Try them on a few devices first, keep laptops on power,
and only turn on firmware for groups where you want it.

## Disk space and backups

The repository is kept on the server's data disk; the page shows its size and
the free space. A download stops when less than 2 GB would be left. The
repository is not part of the backup, because everything in it can be
downloaded again: after a restore, the server downloads the repository
updates once more by itself.

The files are stored under their SHA-256, and devices fetch them by that
checksum. A separate repository or caching server (planned after version
1.0) can then serve the same files close to the devices.

## Permissions

- **Add drivers and firmware to the repository** (add, reject, remove,
  upload packages, vendor catalog settings): administrators.
- **Install now** on a device: anyone who may manage devices.
- Seeing the lists: anyone who may see devices (limited to their groups).
