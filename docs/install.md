# Installation

Dutch version: [install.nl.md](install.nl.md).

FleetWarden runs on Debian 13 (trixie, amd64). There are two ways to set it up:

| | `install.sh` | Proxmox LXC template |
|---|---|---|
| Where | Any Debian 13: virtual machine, physical server or existing container | Proxmox VE |
| What you do | Run one command | Download the template, create a container |
| Listens on | `127.0.0.1:8080` (choose with `--listen`) | `0.0.0.0:8080` |

Both install the same server package, PostgreSQL and the in-app updater, so
updates, backups and channel switching under **About** work the same way.

Sizing (from [load-test.md](load-test.md#sizing)): up to 250 devices 2 vCPU,
4 GB memory, 40 GB disk; up to 1,000 devices 4 vCPU, 8 GB memory, 80 GB disk.
Disk mostly goes to software uploads, driver packages and local backups. The
installer warns below 2 GB memory or 10 GB free disk.

## Debian 13 VM or physical server: install.sh

Start from a clean Debian 13 install (the "standard system utilities" are
enough; no desktop). As root, or with sudo:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install.sh | sudo bash
```

Test channel instead of production:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/test/installer/install.sh | sudo bash -s -- --channel test
```

The installer says whether it runs on a virtual machine, a physical machine or
a container, warns when memory or disk is below the recommendation, installs
PostgreSQL and the newest server package of the channel, and prints the
one-time `admin` password at the end.

Options:

| Option | Meaning |
|---|---|
| `--channel production\|test` | Release channel (default `production`) |
| `--listen 0.0.0.0:8080` | Listen on every interface. Use it when the reverse proxy runs on another machine (default `127.0.0.1:8080`) |
| `--trusted-proxies 192.168.1.10` | IPs or CIDRs of your reverse proxy, comma-separated, so the real client IP is logged |
| `--deb PATH` | Install a downloaded package instead of the newest release |
| `--yes` | Do not ask for confirmation |

Running the installer again on an installed server is safe: it keeps the
database and configuration and only applies `--listen` / `--trusted-proxies`.

Example with Zoraxy on 192.168.1.10:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install.sh \
  | sudo bash -s -- --listen 0.0.0.0:8080 --trusted-proxies 192.168.1.10 --yes
```

Then put the server behind your reverse proxy: [reverse-proxy.md](reverse-proxy.md).

## Proxmox: LXC container template

The template is a Debian 13 container with PostgreSQL and FleetWarden ready to
go. Nothing unique is baked in: SSH host keys, the database, its password and
the admin account are created at the first start of each container.

### Download URL

| Channel | URL |
|---|---|
| Test | `https://github.com/rhisijmuiden/fleetwarden/releases/download/lxc-test/fleetwarden-debian-13-lxc_amd64.tar.zst` |
| Production | `https://github.com/rhisijmuiden/fleetwarden/releases/latest/download/fleetwarden-debian-13-lxc_amd64.tar.zst` |

These URLs always point to the newest build. Every release also carries the
template under its own version (`releases/download/v<version>/...`).

### 1. Download the template into Proxmox

1. In the Proxmox web interface select your node, then a storage that holds
   container templates (usually **local**).
2. Open **CT Templates** and click **Download from URL**.
3. Paste the URL from the table and click **Query URL**.
4. Optionally give the file a name with the version, for example
   `fleetwarden-1.1.0-debian-13_amd64.tar.zst` (it must end in `.tar.zst`).
5. Click **Download**.

From the Proxmox shell instead:

```sh
wget -O /var/lib/vz/template/cache/fleetwarden-debian-13-lxc_amd64.tar.zst \
  https://github.com/rhisijmuiden/fleetwarden/releases/download/lxc-test/fleetwarden-debian-13-lxc_amd64.tar.zst
```

### 2. Create a container

Click **Create CT** (top right):

- **General**: hostname (for example `fleetwarden`), a root password and/or
  SSH key. Keep **Unprivileged container** and **Nesting** ticked (the default).
- **Template**: the storage from step 1 and the FleetWarden template.
- **Disks**: 40 GB (80 GB for 1,000 devices).
- **CPU**: 2 cores (4 for 1,000 devices). **Memory**: 4096 MB (8192 MB for
  1,000 devices), swap 512 MB.
- **Network**: your bridge, IPv4 DHCP or a static address. A static address
  (or a DHCP reservation) is easiest for the reverse proxy.
- **DNS**: use host settings, or fill in your DNS server.
- **Confirm**: tick **Start after created**.

Or from the Proxmox shell:

```sh
pct create 120 local:vztmpl/fleetwarden-debian-13-lxc_amd64.tar.zst \
  --hostname fleetwarden --cores 2 --memory 4096 --swap 512 \
  --rootfs local-lvm:40 --net0 name=eth0,bridge=vmbr0,ip=dhcp \
  --unprivileged 1 --features nesting=1 --onboot 1 --password 'choose-a-root-password' --start 1
```

### 3. First start

The first start takes about a minute. When it is done the container's
**Console** shows only:

```
  URL:       http://192.168.1.50:8080
  Username:  admin
  Password:  xxxxx-xxxxx-xxxxx-xxxxx
```

Open the URL, sign in and choose your own password; the password then
disappears from the console. Afterwards put the container behind your
reverse proxy ([reverse-proxy.md](reverse-proxy.md)) and add the proxy's IP
under **Administration > Reverse proxy > Trusted proxies**.

The container listens on port 8080 on every interface, because the reverse
proxy usually runs elsewhere. Only allow port 8080 from the proxy (Proxmox
firewall on the container, or `FW_LISTEN` in `/etc/fleetwarden/server.env`).

Good to know:

- Updates, channel switching and backups work from **About** and
  **Administration > Backup & restore** just like on a VM.
- First start log: `journalctl -u fleetwarden-firstboot` or
  `/var/log/fleetwarden-firstboot.log` (root only).
- Lost the admin password: `pct enter <id>` and run
  `fleetwarden-server reset-admin`.
- Privileged containers work too. Without **Nesting** the hardened service
  units cannot start; turn it on under **Options > Features**.
- Back up the container with Proxmox (vzdump) as well as with FleetWarden's own
  backups to FTP/FTPS/SCP: the FleetWarden backup is what you restore on a
  new server.

## Building the template yourself

CI builds the template for every build (job "Proxmox LXC template"), boots it
with systemd and checks the first start. Locally, as root on Debian 13:

```sh
packaging/lxc/build-template.sh dist/fleetwarden-server_<version>_amd64.deb out/fleetwarden-debian-13-lxc_amd64.tar.zst
sudo tests/lxc-template-test.sh out/fleetwarden-debian-13-lxc_amd64.tar.zst
```
