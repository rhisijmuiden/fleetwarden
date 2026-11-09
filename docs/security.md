# Security status

*Nederlands: [security.nl.md](security.nl.md)*

Open **Security** in the menu to see, for every device, whether its disks are
encrypted, whether antivirus is on and up to date, whether the firewall is on,
which threats Microsoft Defender found, whether Secure Boot and the TPM are on,
and who is a local administrator. The tiles at the top count the problems;
click one to list only the devices that have it. Each device also has a
**Security** tab.

The agent sends its security status after every inventory (default every 15
minutes, see **Settings > Agent scans**) and when you click **Check now** on the
device. Agents older than this version send nothing until they are updated.

## What is read

| | Windows | Linux |
|---|---|---|
| Disk encryption | BitLocker per fixed drive (`Get-BitLockerVolume`): status, percentage, protection, key protectors | Mounted file systems and whether LUKS / dm-crypt lies under them (`lsblk`) |
| Recovery keys | BitLocker recovery passwords, stored encrypted on the server | – |
| Antivirus | Products registered with Windows Security Center, plus Microsoft Defender (mode, real-time and tamper protection, signatures, last scans, detections of the last 30 days) | Microsoft Defender for Endpoint (`mdatp`) and ClamAV |
| Firewall | Domain, Private and Public profile (effective policy) and the current network | Proxmox VE firewall, ufw, firewalld, or a filtering nftables/iptables ruleset |
| Local administrators | Members of the Administrators group (also accounts that no longer exist) | Members of sudo, wheel and admin, and accounts with UID 0 |
| Secure Boot, TPM | `Confirm-SecureBootUEFI`, `Win32_Tpm` | EFI variable `SecureBoot`, `/sys/class/tpm` |

How the summary is worked out:

- **Encrypted**: the system drive is encrypted and protected. **Partly
  encrypted**: the system drive is, but another fixed drive is not (on Linux
  only `/home` counts next to `/`). **Suspended**: BitLocker protection is
  paused, for example for a BIOS update; the key is readable from the drive.
- **Antivirus OK**: at least one product is on and up to date. Linux devices
  without antivirus show "None" in grey and are not counted as a problem.
- **Firewall partly off**: at least one Windows profile is off.

## BitLocker recovery keys

The agent sends the recovery passwords of every BitLocker drive. The server
encrypts them with its secret key (the same key that protects the mail and SSO
secrets, so it is in every backup) and never puts them in the stored report.
Keys stay findable when they are replaced or when the device is deleted, so an
old printout or a reinstalled PC can still be looked up.

To help a user at the BitLocker recovery screen:

1. Open **Security > Recovery keys**.
2. Type the first characters of the **key ID** the recovery screen shows, the
   computer name or the serial number.
3. Click **Show**, optionally type a reason (ticket number), and read the 48
   digits to the user.
4. Once the user is back in Windows, click **New recovery key**. The device
   adds a new recovery password, removes the old ones and reports the new key
   right away. On domain-joined devices the new key is also backed up to Active
   Directory, on Entra-joined devices to Entra ID, when that is set up.

Showing a key is written to the audit log with your name and the reason.
Administrators and technicians have the permission **security.keys**; read-only
users see the status but no keys. Users limited to device groups only see the
keys of devices in their device groups; keys of deleted devices are for
unscoped users.

## Defender actions

On Windows devices the **Security** tab has **Quick scan**, **Full scan** and
**Update signatures** (permission devices.manage). An offline device runs the
action when it comes back within a day. A full scan may take hours; the
action is marked failed when no answer came within 13 hours. The device sends
a fresh security status when an action is done.

## Alerts

Under **Alerts > Rules** four rule kinds use the security status:

- **Threat detected**: Defender reports a threat it did not remove or
  quarantine. A rule of this kind exists from the start.
- **Antivirus off or out of date** (Windows only).
- **Firewall off**: a profile or the Linux firewall is off.
- **System drive not encrypted**: not encrypted, partly, or suspended. Limit
  this one to workstations if your servers are not encrypted.

Alerts close by themselves once the device reports the problem is gone.
