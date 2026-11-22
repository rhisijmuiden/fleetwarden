# Wijzigingen

Alle belangrijke wijzigingen in FleetWarden. Engelse versie: [CHANGELOG.md](CHANGELOG.md).
Versienummers volgen [semantic versioning](https://semver.org/lang/nl/);
testversies verschijnen vóór een release als `X.Y.Z-test.N`.

## Nog niet uitgebracht

### Toegevoegd
- MCP-server voor AI-assistenten zoals Claude (`/mcp`, standaard uit):
  37 tools om apparaten, updates, kwetsbaarheden, beveiliging, meldingen,
  software en assets op te zoeken en te beheren. Assistenten handelen als een
  FleetWarden-gebruiker (rol en apparaatgroepen gelden), elke aanroep staat in
  het auditlogboek, toegangsniveaus lezen / uitvoeren / volledig (ingrijpende
  tools vragen de hostnaam als bevestiging). Persoonlijke tokens en OAuth
  2.1-aanmelding voor claude.ai. Beheer > AI-assistenten (MCP); eigen tokens
  onder Profiel. Zie docs/mcp.nl.md.
- Proxmox LXC-containertemplate (Debian 13, `.tar.zst`). Download het via
  CT Templates > Download from URL, maak een container aan en FleetWarden
  draait: de eerste start maakt de SSH-sleutels, database en het admin-account
  aan, en de console toont alleen de URL, gebruikersnaam en het eenmalige
  wachtwoord. Gebouwd en opgestart getest in CI; het testkanaal heeft een vaste
  URL (release `lxc-test`), productie gebruikt `releases/latest/download/`.
  Zie docs/install.nl.md.
- install.sh: `--listen` en `--trusted-proxies` voor een reverse proxy op een
  andere machine, en een controle of hij op een VM, fysieke machine of
  container draait, met waarschuwingen bij te weinig geheugen of schijfruimte.

### Gewijzigd
- Nieuwe huisstijl: IJmond360 (paars, roze en aubergine, Inter met Epilogue
  voor paginatitels, het IJmond360-logo). Die vervangt de blauwe
  FleetWarden-stijl in de webinterface, e-mails, PDF-rapporten en de
  Windows-traypop-up. De oude stijl blijft in branding/fleetwarden/.
  Eigen kleuren onder Beheer > Huisstijl gaan er nog steeds boven.

### Opgelost
- Goedgekeurde agentversies kwamen niet aan bij agents op 1.0.0: testversies
  heetten nog 1.0.0-test.N, wat lager sorteert dan de release 1.0.0, dus die
  agents telden als bijgewerkt. De versie is nu 1.1.0, dus testversies heten
  1.1.0-test.N. De goedkeurkaart zegt hoeveel agents een versie bijwerkt en
  waarom andere blijven staan (een nieuwere versie, of een ander kanaal).

### Toegevoegd
- Agent-updates met goedkeuring: Agent-updates toont de 5 nieuwste
  agentversies; keur er één goed en elke agent met een oudere versie werkt
  bij naar die versie, zonder uitrolringen. Dit is nu de standaard; ring voor
  ring uitrollen blijft beschikbaar als instelling. Zie docs/agents.nl.md.
- Chatten met de aangemelde gebruiker: een chatpaneel op de apparaatpagina en
  in hulpvragen, een chatvenster in de tray, afleverstatus, automatisch einde
  na een uur, en het verslag als notitie bij het gekoppelde servicedeskticket.
  Onbeheerd overnemen per apparaatgroep: overnemen verbindt als niemand de
  toestemmingsvraag beantwoordt. Beide zijn ticketlink-acties. Zie
  docs/selfservice.nl.md en docs/remote.nl.md.
- Tickets vanuit de tray: het hulpformulier wordt een ticketformulier met een
  verplichte omschrijving, urgentie en categorie. Een ticket gaat naar
  FleetWarden, de eigen Outlook van de gebruiker (met het apparaatrapport als
  bijlage), de mailserver of een servicedeskkoppeling, met een apparaatrapport
  (hardware, IP, herstart nodig, wachtende updates, catalogussoftware,
  beveiliging, waarschuwingen) en een link die het apparaat in FleetWarden
  opent met overnemen, terminal of scan. Ticketnummer en status uit de
  servicedesk, antwoorden als notitie, een voorbeeld in de instellingen, en
  links die de aanmelding overleven. Zie docs/selfservice.nl.md.
- Koppelingen (Administratie > Koppelingen): kant-en-klare koppelingen met
  TOPdesk, Freshservice, Jira Service Management, ServiceNow, Zendesk,
  HaloITSM, Zammad en GLPI (ticket met aanmelder, prioriteit, interne notities,
  status), en eigen API-koppelingen met methode, URL, authenticatie (bearer,
  Basic, API-sleutel, OAuth 2.0 client credentials, HMAC), headers en een
  body-sjabloon met variabelen. Nieuwe waarschuwingen en nieuwe apparaten
  kunnen ze starten; verbinding testen, testticket en een logboek van 30 dagen.
  Zie docs/integrations.nl.md.
- Distributieservers voor vestigingen (Administratie > Distributieservers): een cache
  die geüploade installatiebestanden, drivers en firmware en agentpakketten
  bewaart en uitlevert aan de agents in zijn netwerken of apparaatgroepen. De
  beheerserver beslist nog steeds per download; agents pinnen het certificaat,
  controleren de SHA-256 van elk bestand en vallen terug op de beheerserver.
  Installatie met een eenmalig token, optionele bandbreedtelimiet, bijwerken
  vanuit de webinterface. Nieuw pakket `fleetwarden-distribution` en
  `install-distribution.sh`; zie docs/distribution.nl.md.

## 1.0.0 (2026-11)

De eerste productierelease. Alles hieronder is gebouwd en getest op het
testkanaal in de 0.1-reeks.

### Platform
- Installer voor Debian 13, systemd-services, server en agents bijwerken vanuit
  de webinterface met een productie- en een testkanaal, automatisch
  terugdraaien.
- Lokale accounts met argon2id, TOTP, rollen (beheerder, technicus,
  alleen-lezen) beperkt tot apparaatgroepen, nood-beheerder; optioneel inloggen
  met Microsoft Entra ID met rolkoppeling.
- Eigen SMTP-serverinstellingen (STARTTLS/TLS, maillog met opnieuw versturen),
  huisstijl en volledig white-label, Engelse en Nederlandse interface,
  auditlog.
- Backups naar FTP, FTPS, SCP (SFTP) en OneDrive, volledig en incrementeel,
  versleuteld, met herstel vanaf de externe locatie.
- Reverse-proxypagina met kant-en-klare configuraties en een test van begin
  tot eind; secure gateway voor agents op internet.

### Endpoints
- Windows-agent (MSI met wizard, stille installatie, tray-app) en Linux-agent
  (.deb, Debian/Ubuntu/Proxmox VE); wizard Add client met
  enrollment-tokens; agent-updates in ringen met terugdraaien; instelbare
  scanintervallen.
- Inventaris van hardware en software, apparaatgroepen, assetbeheer
  (eigenaar, locatie, garantie, softwarelicenties).

### Beheer
- Patchbeheer voor Windows Update en apt met beleid, goedkeuringen,
  onderhoudsvensters en meldingen aan gebruikers.
- Repository voor drivers en firmware (Windows Update, Dell, HP, Lenovo, fwupd,
  eigen pakketten), alleen na goedkeuring.
- Software uitrollen uit winget en eigen pakketten, verwijderen, browsers
  geforceerd bijwerken met herstel van de sessie.
- Configuratiebeheer: browserbeleid, register, scripts, schijf- en
  printerkoppelingen, Windows-instellingen, profielen per groep.
- Overnemen op afstand in de browser, terminal op afstand, processen,
  services, logboeken en bestandsoverdracht zonder het scherm over te nemen.
- Energiebeheer: Wake-on-LAN, geplande herstarts en afsluiten.
- Selfservice vanuit de tray-app: softwarecatalogus, updates, hulpvragen.

### Inzicht
- Beveiligingsstatus: BitLocker-herstelsleutels, Defender, firewall, lokale
  beheerders.
- Kwetsbaarheden (NVD, CISA KEV, Microsoft, Debian) met uitrol van de oplossing
  en geaccepteerde risico's.
- Rapporten met PDF/CSV-export en geplande mails; meldingen.

### Hardening voor 1.0 (fase 17)
- Security-review, zie [docs/hardening.nl.md](docs/hardening.nl.md): Go 1.26.10
  (41 kwetsbaarheden in de standaardbibliotheek opgelost), `GROUP=` bij
  inschrijven beperkt tot de groepen van de maker van het token, een limiet op
  gelijktijdige wachtwoordberekeningen, een limiet op het uitzetten van TOTP,
  strengere systemd-sandbox, extra beveiligingsheaders, en govulncheck en npm
  audit in CI.
- Belastingtest met 2.000 gesimuleerde agents, zie
  [docs/load-test.nl.md](docs/load-test.nl.md): agents die hun verbinding
  konden verbreken als de server meerdere berichten tegelijk stuurde opgelost,
  geheugen van de server met 2.000 agents van 3,1 GB naar 0,2 GB,
  kwetsbaarheidspagina's tot zeven keer sneller, tuning van databasepool en
  indexen.
- Documentatie compleet in het Engels en Nederlands.
