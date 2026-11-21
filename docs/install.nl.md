# Installatie

Engelse versie: [install.md](install.md).

FleetWarden draait op Debian 13 (trixie, amd64). Je kunt het op twee manieren
opzetten:

| | `install.sh` | Proxmox LXC-template |
|---|---|---|
| Waar | Elke Debian 13: virtuele machine, fysieke server of bestaande container | Proxmox VE |
| Wat je doet | Eén commando uitvoeren | Template downloaden, container aanmaken |
| Luistert op | `127.0.0.1:8080` (te kiezen met `--listen`) | `0.0.0.0:8080` |

Beide installeren hetzelfde serverpakket, PostgreSQL en de updater, dus
updates, back-ups en wisselen van kanaal onder **Over** werken hetzelfde.

Afmetingen (uit [load-test.nl.md](load-test.nl.md#dimensionering)): tot 250
apparaten 2 vCPU, 4 GB geheugen, 40 GB schijf; tot 1.000 apparaten 4 vCPU,
8 GB geheugen, 80 GB schijf. Schijfruimte gaat vooral naar geüploade software,
driverpakketten en lokale back-ups. De installer waarschuwt onder 2 GB geheugen
of 10 GB vrije schijfruimte.

## Debian 13 VM of fysieke server: install.sh

Begin met een schone Debian 13 (de "standaard systeemhulpmiddelen" zijn genoeg,
geen desktop). Als root of met sudo:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install.sh | sudo bash
```

Testkanaal in plaats van productie:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/test/installer/install.sh | sudo bash -s -- --channel test
```

De installer meldt of hij op een virtuele machine, een fysieke machine of een
container draait, waarschuwt als geheugen of schijf onder het advies zit,
installeert PostgreSQL en het nieuwste serverpakket van het kanaal, en toont
aan het eind het eenmalige wachtwoord van `admin`.

Opties:

| Optie | Betekenis |
|---|---|
| `--channel production\|test` | Releasekanaal (standaard `production`) |
| `--listen 0.0.0.0:8080` | Luister op alle netwerkkaarten. Gebruik dit als de reverse proxy op een andere machine draait (standaard `127.0.0.1:8080`) |
| `--trusted-proxies 192.168.1.10` | IP's of CIDR's van je reverse proxy, gescheiden door komma's, zodat het echte IP van de gebruiker gelogd wordt |
| `--deb PAD` | Een gedownload pakket installeren in plaats van de nieuwste release |
| `--yes` | Niet om bevestiging vragen |

De installer nogmaals draaien op een geïnstalleerde server kan geen kwaad: de
database en configuratie blijven staan, alleen `--listen` / `--trusted-proxies`
worden toegepast.

Voorbeeld met Zoraxy op 192.168.1.10:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install.sh \
  | sudo bash -s -- --listen 0.0.0.0:8080 --trusted-proxies 192.168.1.10 --yes
```

Zet de server daarna achter je reverse proxy: [reverse-proxy.nl.md](reverse-proxy.nl.md).

## Proxmox: LXC-containertemplate

Het template is een Debian 13-container met PostgreSQL en FleetWarden klaar
voor gebruik. Er zit niets unieks in: SSH-sleutels, de database, het
databasewachtwoord en het admin-account worden bij de eerste start van elke
container aangemaakt.

### Download-URL

| Kanaal | URL |
|---|---|
| Test | `https://github.com/rhisijmuiden/fleetwarden/releases/download/lxc-test/fleetwarden-debian-13-lxc_amd64.tar.zst` |
| Productie | `https://github.com/rhisijmuiden/fleetwarden/releases/latest/download/fleetwarden-debian-13-lxc_amd64.tar.zst` |

Deze URL's wijzen altijd naar de nieuwste build. Elke release bevat het
template ook onder zijn eigen versie (`releases/download/v<versie>/...`).

### 1. Template in Proxmox downloaden

1. Kies in de Proxmox-webinterface je node en daarna een opslag voor
   containertemplates (meestal **local**).
2. Open **CT Templates** en klik op **Download from URL**.
3. Plak de URL uit de tabel en klik op **Query URL**.
4. Geef het bestand eventueel een naam met de versie, bijvoorbeeld
   `fleetwarden-1.1.0-debian-13_amd64.tar.zst` (moet eindigen op `.tar.zst`).
5. Klik op **Download**.

Of vanuit de Proxmox-shell:

```sh
wget -O /var/lib/vz/template/cache/fleetwarden-debian-13-lxc_amd64.tar.zst \
  https://github.com/rhisijmuiden/fleetwarden/releases/download/lxc-test/fleetwarden-debian-13-lxc_amd64.tar.zst
```

### 2. Container aanmaken

Klik op **Create CT** (rechtsboven):

- **General**: hostnaam (bijvoorbeeld `fleetwarden`), een rootwachtwoord en/of
  SSH-sleutel. Laat **Unprivileged container** en **Nesting** aangevinkt (standaard).
- **Template**: de opslag uit stap 1 en het FleetWarden-template.
- **Disks**: 40 GB (80 GB voor 1.000 apparaten).
- **CPU**: 2 cores (4 voor 1.000 apparaten). **Memory**: 4096 MB (8192 MB voor
  1.000 apparaten), swap 512 MB.
- **Network**: je bridge, IPv4 DHCP of een vast adres. Een vast adres (of een
  DHCP-reservering) is het makkelijkst voor de reverse proxy.
- **DNS**: instellingen van de host gebruiken, of je DNS-server invullen.
- **Confirm**: vink **Start after created** aan.

Of vanuit de Proxmox-shell:

```sh
pct create 120 local:vztmpl/fleetwarden-debian-13-lxc_amd64.tar.zst \
  --hostname fleetwarden --cores 2 --memory 4096 --swap 512 \
  --rootfs local-lvm:40 --net0 name=eth0,bridge=vmbr0,ip=dhcp \
  --unprivileged 1 --features nesting=1 --onboot 1 --password 'kies-een-rootwachtwoord' --start 1
```

### 3. Eerste start

De eerste start duurt ongeveer een minuut. Daarna toont de **Console** van de
container alleen:

```
  URL:       http://192.168.1.50:8080
  Username:  admin
  Password:  xxxxx-xxxxx-xxxxx-xxxxx
```

Open de URL, log in en kies je eigen wachtwoord; het wachtwoord verdwijnt dan
van de console. Zet de container daarna achter je reverse proxy
([reverse-proxy.nl.md](reverse-proxy.nl.md)) en voeg het IP van de proxy toe
onder **Administratie > Reverse proxy > Vertrouwde proxy's**.

De container luistert op poort 8080 op alle netwerkkaarten, omdat de reverse
proxy meestal ergens anders draait. Sta poort 8080 alleen toe vanaf de proxy
(Proxmox-firewall op de container, of `FW_LISTEN` in `/etc/fleetwarden/server.env`).

Goed om te weten:

- Updates, wisselen van kanaal en back-ups werken via **Over** en
  **Administratie > Backup & herstel**, net als op een VM.
- Log van de eerste start: `journalctl -u fleetwarden-firstboot` of
  `/var/log/fleetwarden-firstboot.log` (alleen root).
- Admin-wachtwoord kwijt: `pct enter <id>` en dan
  `fleetwarden-server reset-admin`.
- Privileged containers werken ook. Zonder **Nesting** kunnen de beveiligde
  service-units niet starten; zet het aan onder **Options > Features**.
- Maak naast de eigen back-ups van FleetWarden (FTP/FTPS/SCP) ook een
  Proxmox-back-up (vzdump): de FleetWarden-back-up zet je terug op een nieuwe server.

## Het template zelf bouwen

CI bouwt het template bij elke build (job "Proxmox LXC template"), start het
op met systemd en controleert de eerste start. Lokaal, als root op Debian 13:

```sh
packaging/lxc/build-template.sh dist/fleetwarden-server_<versie>_amd64.deb out/fleetwarden-debian-13-lxc_amd64.tar.zst
sudo tests/lxc-template-test.sh out/fleetwarden-debian-13-lxc_amd64.tar.zst
```
