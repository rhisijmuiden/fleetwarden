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

Dell, HP and Lenovo catalogs (BIOS and vendor drivers) and uploading your own
driver packages follow in a later update.

## How it works

1. Devices in a group with a patch policy scan at the interval under
   **Settings > Agent scans**. Each scan also lists driver and firmware
   updates. They show under **Drivers & firmware > Available**, with the
   number of devices that can install them; **Scan now** on a device scans
   at once.
2. Select updates and choose **Add to repository**. The server downloads the
   files from the vendor (only from Microsoft's and LVFS's download servers),
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

- **Add drivers and firmware to the repository** (add, reject, remove):
  administrators.
- **Install now** on a device: anyone who may manage devices.
- Seeing the lists: anyone who may see devices (limited to their groups).
