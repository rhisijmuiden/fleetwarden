# Backup and restore

[Nederlands](backup.nl.md)

FleetWarden backs up its database and server files to one or more locations
and restores them from the web interface, on the same server or on a new one.
Everything is under **Administration > Backup & restore** (administrators only).

## What a backup contains

- The PostgreSQL database: users, roles, settings, audit log, backup locations,
  and in later phases devices, inventory, policies and jobs. Active sessions and
  the backup history are left out.
- The data directory `/var/lib/fleetwarden`: the secret key that encrypts stored
  passwords, and uploaded files such as installers.
- A copy of `/etc/fleetwarden/server.env` for reference.

## How it works

- **Full and incremental.** A full backup uploads everything. An incremental
  backup splits the data into content-defined chunks and only uploads chunks that
  changed since the last full backup of the same chain. Every backup can be
  restored on its own; you never have to apply a chain by hand.
- **Encrypted before it leaves the server.** Each chunk is compressed (zstd) and
  encrypted with AES-256-GCM. The key is protected by your passphrase
  (Argon2id). The storage provider only sees random data.
- **Verified on restore.** Every chunk is authenticated and the whole backup is
  checked against its SHA-256 checksum before anything is changed.
- **Retention.** A new full backup starts a new chain every *N* days; the newest
  *K* chains are kept and older ones are deleted with their incrementals.

## 1. Set the passphrase

Open the tab **Schedule & encryption** and choose a passphrase of at least 12
characters. **Store it in your password manager**: without it nobody can
restore the backups, including you. Changing it later re-keys the backups at
all reachable locations.

## 2. Add locations

| Type | Use for | Notes |
| --- | --- | --- |
| SCP / SFTP | NAS (Synology, QNAP), Linux server, hosting | Password or SSH key. FleetWarden can generate an Ed25519 key pair; add the public key to `~/.ssh/authorized_keys` of the backup user. |
| FTPS | FTP server with TLS (FileZilla Server, NAS) | Explicit TLS on port 21 (default) or implicit TLS on 990. |
| FTP | Old servers only | The backup is encrypted, the password is not. |
| OneDrive | Microsoft 365 | See below. |
| Local folder | A mounted network share | A sub-folder of `/var/lib/fleetwarden/backups`. Mount the share there. |

Press **Test connection**: FleetWarden connects, writes and deletes a test
file and looks for existing backups. The first time it shows the SSH host key
or (for a self-signed FTPS certificate) the certificate fingerprint; compare it
with the server and press **Trust**. A changed key later stops backups until you
trust it again.

### Synology example (SFTP)

1. Control Panel > File Services > FTP: enable **SFTP service**.
2. Create a user `fleetwarden-backup` with access to one shared folder, e.g.
   `backup`.
3. In FleetWarden: type SCP / SFTP, server = NAS address, port 22, user
   `fleetwarden-backup`, folder `backup/fleetwarden`.

### OneDrive (Microsoft 365)

OneDrive uses an app registration with application permissions, so no user has
to sign in. You can use the same app registration as for Entra sign-in.

1. [Entra admin center](https://entra.microsoft.com) > Applications > App
   registrations > your FleetWarden app (or **New registration**).
2. **API permissions** > Add a permission > Microsoft Graph > **Application
   permissions** > `Files.ReadWrite.All` > Add. Then **Grant admin consent**.
3. **Certificates & secrets** > New client secret; copy the value.
4. In FleetWarden: type OneDrive, fill in the tenant ID, client ID and secret,
   and the user principal name of the account whose OneDrive stores the backups
   (for example a dedicated `backup@yourdomain.com` with a OneDrive licence).

`Files.ReadWrite.All` gives the app access to every OneDrive in the tenant.
Keep the client secret safe and use a dedicated app registration if you prefer
to keep sign-in and backups apart.

## 3. Schedule

Choose how often (every hour up to daily), from what time, how often a new full
backup starts and how many full backups to keep. Example: daily at 02:00, a
full backup every 7 days, keep 4: you can go back about four weeks. A backup
that was missed because the server was off runs once when it is back. When a
backup fails, all administrators with an e-mail address get a message (set up
the mail server first).

**Back up now** runs a backup immediately; **Full backup now** starts a new chain.

## Restore on the same server

1. Tab **Restore**, choose the location and press **Find backups**.
2. Choose a backup, check the time and version, press **Restore this backup** and
   type `RESTORE`.
3. The server downloads and verifies the backup, then the helper service
   `fleetwarden-restore` stops the server, restores the database in a single
   transaction, replaces the data directory and starts the server again.
4. Everyone is signed out. Sign in with an account from the backup.

The previous state is kept in `/var/lib/fleetwarden/restore/rollback` until the
next restore. If anything fails, the previous state is put back automatically.
Backups made with a newer FleetWarden version cannot be restored on an older
server: update the server first.

## Restore on a new server (disaster recovery)

1. Install FleetWarden on a clean Debian 13 server (see the README) and sign in
   with the one-time password.
2. Go to **Backup & restore > Restore**, choose **Another location**, enter the
   location of the old server and the backup passphrase, and press
   **Find backups**.
3. Choose the newest backup and restore it.
4. After the restart, sign in with your old account. Backup locations, the
   passphrase and the schedule come back with the backup.

Network settings of the new server (`/etc/fleetwarden/server.env`) are not
changed. The old file is saved as
`/var/lib/fleetwarden/server.env.from-backup` for comparison.

## Command line

```sh
sudo fleetwarden-server backup                # back up to all enabled locations now
sudo fleetwarden-server backup --full         # start a new full backup
sudo fleetwarden-server restore-apply         # apply a restore staged in the UI by hand
```

`restore-apply` is normally started by `fleetwarden-restore.path`; you only need
it on a system without systemd.
