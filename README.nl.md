# FleetWarden

**Zelf-gehoste, agent-based endpoint management voor Windows en Linux.**

[English](README.md)

FleetWarden is een gratis, source-available alternatief voor tools als Action1
en ManageEngine Endpoint Central. Het draait volledig op je eigen Debian 13
server en beheert Windows-werkplekken, Windows-servers, Debian-machines en
Proxmox VE-nodes via een lichte agent.

> **Status:** vroege ontwikkeling (fase 0, fundament). Nog niet geschikt voor productie.

## Installatie

Op een kale Debian 13 (amd64) server:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install.sh | sudo bash
```

Met `-s -- --channel test` installeer je de nieuwste testversie. Zet de server
daarna achter je reverse proxy, zie [docs/reverse-proxy.md](docs/reverse-proxy.md).

## Licentie

FleetWarden valt onder de **GNU AGPL v3.0 met de Commons Clause**. Je mag het
gratis gebruiken, aanpassen en zelf hosten, ook binnen je bedrijf. Wijzigingen
moeten onder dezelfde voorwaarden open blijven. Je mag FleetWarden **niet**
verkopen of er een betaald product of betaalde dienst mee aanbieden. Zie
[LICENSE](LICENSE).
