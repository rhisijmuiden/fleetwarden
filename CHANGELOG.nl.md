# Wijzigingen

Alle belangrijke wijzigingen in FleetWarden. Engelse versie: [CHANGELOG.md](CHANGELOG.md).
Versienummers volgen [semantic versioning](https://semver.org/lang/nl/);
testversies verschijnen vóór een release als `X.Y.Z-test.N`.

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
