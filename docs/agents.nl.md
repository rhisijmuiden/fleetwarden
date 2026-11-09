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

## Windows-installer (MSI)

De MSI installeert `C:\Program Files\FleetWarden Agent\fleetwarden-agent.exe`
en de service `FleetWardenAgent` (start automatisch, herstart na een crash).
Start je hem zonder instellingen, dan vraagt de wizard om het serveradres, het
enrollment-token en eventueel een apparaatgroep; **Test connection** controleert
of er een FleetWarden-server antwoordt en toont het TLS-certificaat. Een
download via **Add client** bevat het serveradres en het token al (de server
zet ze erin met `msitools`, dat het serverpakket aanbeveelt).

| Property | Betekenis |
| --- | --- |
| `SERVER` | Serveradres, bijvoorbeeld `https://em.voorbeeld.nl` |
| `TOKEN` | Enrollment-token; komt niet in het installatielog |
| `GROUP` | Naam van de apparaatgroep (optioneel, gaat voor de groep van het token, binnen de groepen van de maker van het token) |
| `FORCEENROLL=1` | Een bestaande inschrijving op deze machine vervangen |

Zonder `SERVER` en `TOKEN` blijft een bestaande configuratie staan, dus
bijwerken is gewoon `msiexec /i fleetwarden-agent_X_x64.msi /qn`. Verwijderen
wist de lokale inschrijving; het apparaat blijft in FleetWarden staan tot je het
verwijdert. De wizard is voorlopig alleen in het Engels.

## Enrollment-tokens

Een token hoort bij één apparaatgroep. Het kan verlopen en is in te trekken
onder **Groepen > Enrollment-tokens**; intrekken stopt alleen nieuwe
inschrijvingen. Met `GROUP=` (msiexec) of `--group` kies je een andere
bestaande groep op naam, maar alleen een groep die de maker van het token mag
beheren: een token van een technicus die beperkt is tot enkele groepen kan
geen apparaat in een andere groep zetten (dan geldt de groep van het token en
staat de poging in het auditlog). Op de serverconsole maak je ook een token aan met
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

## Scanintervallen

**Instellingen > Agentscans** bepaalt hoe vaak agents hun inventaris sturen
(hardware, geïnstalleerde software, aangemelde gebruiker; standaard elke 6 uur,
15 minuten tot 7 dagen) en hoe vaak apparaten met een patchbeleid scannen op
ontbrekende Windows- of apt-updates (standaard elke 6 uur, 1 uur tot 7 dagen).
Bewerk een groep onder **Groepen** om die eigen intervallen te geven. Online
agents krijgen een gewijzigd inventarisinterval binnen een minuut; updatescans
start de server zelf, dus een nieuw updatescaninterval geldt meteen.

## Agent-updates

**Administratie > Agent-updates** toont de agentversies op de server en rolt ze uit:

- Elk uur kijkt de server in de GitHub-releases voor de kanalen die je groepen
  gebruiken (productiegroepen krijgen alleen releases, testgroepen ook
  testbuilds), downloadt de agentpakketten en controleert ze tegen de
  `SHA256SUMS` van de release. **Nu op GitHub kijken** doet dat meteen. Je kunt
  ook een `.msi` of `.deb` met de oorspronkelijke bestandsnaam **uploaden**.
- Een **uitrol** stuurt een versie ring voor ring naar de groepen: eerst ring 0
  (pilot), dan ring 1, 2 en 3. Een ring loopt de wachttijd (standaard 24 uur)
  voordat de volgende begint; ringen zonder apparaten worden overgeslagen. Mislukken
  er in een ring meer apparaten dan toegestaan (standaard 2), dan pauzeert de
  uitrol. Je kunt pauzeren, hervatten, direct naar de volgende ring gaan,
  mislukte apparaten opnieuw proberen of annuleren. Met **Voor elke nieuwe
  release een uitrol starten** aan start een nieuwe release vanzelf een uitrol.
- Agents werken alleen bij binnen het updatevenster van hun groep en gaan nooit
  terug naar een oudere versie.
- De agent downloadt het pakket met een ondertekend verzoek, controleert de
  SHA-256 en geeft het door aan een hulpproces (een kopie van zichzelf) dat
  `msiexec` of `dpkg` uitvoert. Mislukt de installatie of maakt de nieuwe versie
  binnen 5 minuten geen verbinding met de server, dan zet het hulpproces de vorige
  versie terug en staat het apparaat onder **Apparaten die niet konden bijwerken**.
- Gedownloade releases zitten niet in de back-up; de server haalt ze opnieuw op.
  De nieuwste 5 blijven bewaard, plus elke versie die een apparaat nog draait.

## Opdrachtregel

```
fleetwarden-agent status
fleetwarden-agent inventory          # toont wat de agent rapporteert
fleetwarden-agent test-connection --server https://em.voorbeeld.nl
fleetwarden-agent configure --server URL --token TOKEN [--group NAAM] [--force]
```
