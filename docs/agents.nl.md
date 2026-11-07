# Agents en inschrijven

*English: [agents.md](agents.md)*

De FleetWarden-agent draait op elke beheerde machine: als Windows-service
(`FleetWardenAgent`, draait als SYSTEM) of als systemd-service
`fleetwarden-agent` op Debian, Ubuntu en Proxmox VE. Domeinleden en standalone
machines worden op dezelfde manier beheerd; domein en OU staan alleen in de
inventaris.

## Een client toevoegen

Open **Apparaten > Add client** (of **Groepen > Add client** voor een
specifieke groep), kies het platform en de groep en klik op **Installer
maken**. De wizard toont:

- **Windows**, in PowerShell als administrator:
  `iwr 'https://em.voorbeeld.nl/agent/install.ps1?t=TOKEN' -UseBasicParsing | iex`
- **Debian / Proxmox VE**, als root:
  `curl -fsSL https://em.voorbeeld.nl/agent/install.sh | sudo bash -s -- TOKEN`
- Een **download** van de MSI of .deb met het serveradres en het token erin,
  dus dubbelklikken (of `apt install ./bestand.deb`) is genoeg.
- De **msiexec**-regel voor GPO, Intune of een RMM:
  `msiexec /i fleetwarden-agent.msi /qn SERVER=https://em.voorbeeld.nl TOKEN=TOKEN [GROUP=Werkplekken]`

Het nieuwe apparaat verschijnt in de wizard zodra het zich heeft ingeschreven.

Zonder de wizard werken de generieke installers ook interactief: de
MSI-wizard vraagt om het serveradres en het token, en `apt install` vraagt
hetzelfde via debconf. Unattended op Linux:
`sudo FW_SERVER=https://em.voorbeeld.nl FW_TOKEN=TOKEN apt install ./fleetwarden-agent_X_amd64.deb`,
of achteraf `sudo fleetwarden-agent configure --server ... --token ...`.

## Enrollment-tokens

Een token hoort bij één apparaatgroep. Het kan verlopen en is in te trekken
onder **Groepen > Enrollment-tokens**; intrekken stopt alleen nieuwe
inschrijvingen. Met `GROUP=` (msiexec) of `--group` kies je een andere
bestaande groep op naam. Op de serverconsole maak je ook een token aan met
`sudo fleetwarden-server enrollment-token --group Default`.

## Beveiliging

- Bij het inschrijven maakt de agent een Ed25519-sleutelpaar; de privésleutel
  blijft op de machine (`C:\ProgramData\FleetWarden\agent.json`, alleen
  leesbaar voor SYSTEM en Administrators, of
  `/etc/fleetwarden-agent/agent.json`, modus 600).
- De agent pint de eigen Ed25519-sleutel van de server. Elk bericht in beide
  richtingen is ondertekend en genummerd, dus de verbinding blijft echt, ook
  als de reverse proxy TLS afhandelt.
- Agents bouwen zelf de verbinding op via HTTPS/WebSocket op poort 443
  (`/agent/v1/ws`); op de endpoints luistert niets. Zie
  [reverse-proxy.md](reverse-proxy.md) voor de proxy-instellingen.
- Verwijder je een apparaat in de webinterface, dan vergeet een verbonden
  agent zijn inschrijving en stopt hij.

## Apparaatgroepen en rollen

Groepen bepalen de update-ring van de agent, het releasekanaal en een
updatevenster. Technici en alleen-lezen-gebruikers kun je beperken tot
apparaatgroepen (**Gebruikers & rollen**, of per Entra-rolkoppeling);
beheerders zien altijd alle groepen.

## Opdrachtregel

```
fleetwarden-agent status
fleetwarden-agent inventory          # toont wat de agent rapporteert
fleetwarden-agent test-connection --server https://em.voorbeeld.nl
fleetwarden-agent configure --server URL --token TOKEN [--group NAAM] [--force]
```
