# FleetWarden

**Zelf-gehoste, agent-based endpoint management voor Windows en Linux.**

[English](README.md)

FleetWarden is een gratis, source-available alternatief voor tools als Action1
en ManageEngine Endpoint Central. Het draait volledig op je eigen Debian 13
server en beheert Windows-werkplekken, Windows-servers, Debian-machines en
Proxmox VE-nodes via een lichte agent.

> **Status:** versie 1.0. Getest met 2.000 gesimuleerde agents; zie
> [docs/load-test.nl.md](docs/load-test.nl.md) en de security-review in
> [docs/hardening.nl.md](docs/hardening.nl.md).

## Functies

- **Inventaris**: hardware, software, schijven, ingelogde gebruikers, garantie
  en eigenaar; apparaatgroepen en rollen die tot groepen beperkt zijn
- **Patchbeheer**: Windows Update en Debian/Proxmox (apt) met beleid,
  goedkeuringen, onderhoudsvensters en meldingen aan gebruikers met uitstel
- **Drivers en firmware** van Windows Update, Dell, HP, Lenovo en fwupd, via je
  eigen goedgekeurde repository
- **Software uitrollen** uit winget en je eigen MSI/EXE/DEB-pakketten,
  verwijderen en browsers geforceerd bijwerken
- **Configuratiebeheer**: browserbeleid, schijf- en printerkoppelingen,
  register, firewall, energie en andere Windows-instellingen, scripts
- **Overnemen op afstand** in de browser, een terminal op afstand en beheer
  zonder overname (processen, services, logboeken, bestanden)
- **Beveiligingsstatus** (BitLocker-sleutels, Defender, firewall, lokale
  beheerders) en **kwetsbaarheden** (CVE's uit NVD, CISA KEV, Microsoft en
  Debian) met uitrol van de oplossing
- **Energiebeheer**: Wake-on-LAN, geplande herstarts en afsluiten
- **Selfservice** vanuit de tray-app: softwarecatalogus, updates, hulpvragen
- **Rapporten, meldingen en assetbeheer** (licenties, garantie)
- **Secure gateway** voor agents op internet, inloggen met **Entra ID**,
  **backups** (FTP, FTPS, SCP, OneDrive; volledig en incrementeel) met herstel
- Server en agents **bijwerken vanuit de UI** met een productie- en een
  testkanaal
- Engelse en Nederlandse interface, volledig white-label (logo, kleuren)

## Vereisten

- Debian 13 (amd64) met 4 vCPU en 8 GB RAM voor tot 1.000 endpoints (zie
  [dimensionering](docs/load-test.nl.md#dimensionering))
- Een reverse proxy met TLS (Zoraxy, nginx, Caddy, Traefik, ...)
- Agents: Windows 10/11 en Windows Server 2016 of nieuwer; Debian,
  Ubuntu en Proxmox VE

## Installatie

Op een kale Debian 13 (amd64) server:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install.sh | sudo bash
```

Met `-s -- --channel test` installeer je de nieuwste testversie. Zet de server
daarna achter je reverse proxy, zie [docs/reverse-proxy.nl.md](docs/reverse-proxy.nl.md).

De installer toont een eenmalig wachtwoord voor het account `admin`; bij de
eerste login kies je je eigen wachtwoord. Kwijt? Voer
`sudo fleetwarden-server reset-admin` uit.

## Documentatie

- Reverse proxy: [docs/reverse-proxy.nl.md](docs/reverse-proxy.nl.md)
- Beveiliging en checklist voor hardening: [docs/hardening.nl.md](docs/hardening.nl.md)
- Optioneel inloggen met Microsoft Entra ID: [docs/entra-sso.nl.md](docs/entra-sso.nl.md)
- Updates en kanalen: [docs/updates.nl.md](docs/updates.nl.md)
- Backup en herstel: [docs/backup.nl.md](docs/backup.nl.md)
- Agents, Add client en inschrijven: [docs/agents.nl.md](docs/agents.nl.md)
- Patchbeheer: [docs/patching.nl.md](docs/patching.nl.md)
- Drivers en firmware (eigen repository): [docs/drivers.nl.md](docs/drivers.nl.md)
- Configuraties (beleid, koppelingen, Windows-instellingen, scripts): [docs/configurations.nl.md](docs/configurations.nl.md)
- Beveiligingsstatus en BitLocker-herstelsleutels: [docs/security.nl.md](docs/security.nl.md)
- Kwetsbaarheden (CVE's): [docs/vulnerabilities.nl.md](docs/vulnerabilities.nl.md)
- Software uitrollen: [docs/software.nl.md](docs/software.nl.md)
- Selfservice vanuit de tray-app: [docs/selfservice.nl.md](docs/selfservice.nl.md)
- Overnemen op afstand en terminal: [docs/remote.nl.md](docs/remote.nl.md)
- Energiebeheer (Wake-on-LAN, geplande herstarts en afsluiten): [docs/power.nl.md](docs/power.nl.md)
- Beheer zonder overname (processen, services, logboeken, bestanden): [docs/manage.nl.md](docs/manage.nl.md)
- Rapporten en geplande rapportmails: [docs/reports.nl.md](docs/reports.nl.md)
- Meldingen: [docs/alerts.nl.md](docs/alerts.nl.md)
- Assetbeheer (eigenaar, locatie, garantie, softwarelicenties): [docs/assets.nl.md](docs/assets.nl.md)
- Secure gateway in de DMZ voor agents buiten het netwerk: [docs/gateway.nl.md](docs/gateway.nl.md)
- Distributieservers op vestigingen (cache voor installatiebestanden, drivers en agent-updates): [docs/distribution.nl.md](docs/distribution.nl.md)
- Belastingtest en dimensionering: [docs/load-test.nl.md](docs/load-test.nl.md)
- Wat is er veranderd: [CHANGELOG.nl.md](CHANGELOG.nl.md)

## Ontwikkeling

Zie [CONTRIBUTING.md](CONTRIBUTING.md) (Engels) voor het branchmodel (`test`
en `main`), CI en lokale ontwikkeling.

## Licentie

FleetWarden valt onder de **GNU AGPL v3.0 met de Commons Clause**. Je mag het
gratis gebruiken, aanpassen en zelf hosten, ook binnen je bedrijf. Wijzigingen
moeten onder dezelfde voorwaarden open blijven. Je mag FleetWarden **niet**
verkopen of er een betaald product of betaalde dienst mee aanbieden. Zie
[LICENSE](LICENSE).
