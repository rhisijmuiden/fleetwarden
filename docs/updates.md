# Updates and channels

FleetWarden has two update channels:

| Channel | Follows | Releases |
| --- | --- | --- |
| Production | `main` | `vX.Y.Z` |
| Test | `test` | pre-releases `vX.Y.Z-test.N` |

Choose the channel under **Administration > Settings > Updates** or in the
**About** window. Only newer versions are offered: the database schema moves
forward only, so switching from Test to Production waits until Production has a
newer version than the one you run.

## What "Update now" does

The web server runs as the unprivileged `fleetwarden` user and cannot install
packages. It writes the requested version to
`/var/lib/fleetwarden/update/request.json`; the `fleetwarden-update.path` unit
then starts `fleetwarden-update.service` (root), which:

1. downloads the release from GitHub and checks it against `SHA256SUMS`,
2. backs up the database (`pg_dump`), `/etc/fleetwarden` and the secret key to
   `/var/lib/fleetwarden-update/backups/` (the last 3 are kept),
3. installs the package and waits until the server answers with the new version,
4. if that fails: restores the database, reinstalls the previous version and
   reports "rolled back".

The request file only holds a version number; the updater accepts nothing else
and only downloads from the official repository. Progress is in
`/var/lib/fleetwarden-update/status.json` and `journalctl -u fleetwarden-update`.
Administrators with an e-mail address get a mail with the result.

## Debian package versions

Test builds are packaged as `X.Y.Z~test.N`, which Debian sorts below the release
`X.Y.Z`, so a production release always replaces a test build of the same version.
