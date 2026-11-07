# FleetWarden

**Zelf-gehoste, agent-based endpoint management voor Windows en Linux.**

[English](README.md)

FleetWarden is een gratis, source-available alternatief voor tools als Action1
en ManageEngine Endpoint Central. Het draait volledig op je eigen Debian 13
server en beheert Windows-werkplekken, Windows-servers, Debian-machines en
Proxmox VE-nodes via een lichte agent.

> **Status:** vroege ontwikkeling (fase 2: inloggen, rollen, mail, updaten vanuit de UI, back-up en herstel). Nog niet geschikt voor productie.

## Installatie

Op een kale Debian 13 (amd64) server:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install.sh | sudo bash
```

Met `-s -- --channel test` installeer je de nieuwste testversie. Zet de server
daarna achter je reverse proxy, zie [docs/reverse-proxy.md](docs/reverse-proxy.md).

De installer toont een eenmalig wachtwoord voor het account `admin`; bij de
eerste login kies je je eigen wachtwoord. Kwijt? Voer
`sudo fleetwarden-server reset-admin` uit.

- Optioneel inloggen met Microsoft Entra ID: [docs/entra-sso.nl.md](docs/entra-sso.nl.md)
- Updates en kanalen: [docs/updates.md](docs/updates.md)
- Back-up en herstel: [docs/backup.nl.md](docs/backup.nl.md)

## Licentie

FleetWarden valt onder de **GNU AGPL v3.0 met de Commons Clause**. Je mag het
gratis gebruiken, aanpassen en zelf hosten, ook binnen je bedrijf. Wijzigingen
moeten onder dezelfde voorwaarden open blijven. Je mag FleetWarden **niet**
verkopen of er een betaald product of betaalde dienst mee aanbieden. Zie
[LICENSE](LICENSE).
