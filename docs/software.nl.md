# Software uitrollen

*English: [software.md](software.md)*

FleetWarden installeert en verwijdert software op Windows- en Linux-apparaten.
Open **Software** in het menu voor de bibliotheek en de geïnstalleerde
software, en **Uitrollen** voor de taken en hun resultaten.

## De softwarebibliotheek

**Software > Bibliotheek > Pakket toevoegen** biedt drie bronnen:

- **Winget-catalogus**: zoek in de openbare winget-catalogus (dezelfde
  catalogus die `winget` gebruikt, meer dan 15.000 applicaties). FleetWarden
  kiest per apparaat het beste installatiebestand (voor de hele computer, x64
  of arm64, MSI voor EXE) met de stille parameters uit de catalogus. Kies
  *Nieuwste* om altijd de nieuwste versie te installeren, of zet een versie
  vast.
- **Installatiebestand uploaden**: je eigen `.msi` of `.exe` voor Windows, of
  een `.deb` voor Linux, tot 2 GB. FleetWarden herkent Inno Setup- en
  NSIS-installers en vult hun stille parameters in; vul voor andere
  EXE-installers de stille parameters van de leverancier in (bijvoorbeeld
  `/S`, `/quiet` of `/silent`).
- **apt-pakket**: een pakketnaam zoals `htop`, geïnstalleerd uit de
  repository's die het Linux-apparaat al gebruikt.

Onder *Geavanceerd* stel je de naam in waarmee het programma in *Programma's
en onderdelen* wordt herkend, een MSI-productcode en een time-out.

### De winget-catalogus

De server downloadt de catalogusindex (ongeveer 20 MB) één keer per dag van
`cdn.winget.microsoft.com` en bewaart hem in `/var/lib/fleetwarden/catalog`.
**Nu vernieuwen** op de pagina Software laadt hem meteen. De apparaten zelf
hebben geen winget nodig: de agent downloadt het installatiebestand
rechtstreeks van het HTTPS-adres van de leverancier en controleert de
SHA-256-hash uit de catalogus voordat hij het uitvoert. Sommige leveranciers
(bijvoorbeeld Google Chrome) vervangen het bestand achter dezelfde link; zet
voor die pakketten *Gewijzigd installatiebestand toestaan* aan onder
*Geavanceerd*.

Geüploade installatiebestanden staan op de server
(`/var/lib/fleetwarden/software`) en alleen apparaten met een openstaande taak
voor dat bestand kunnen ze downloaden. Ze zitten in de back-up (volledig en
incrementeel), dus een herstel zet ze terug; de winget-catalogus niet, die
wordt opnieuw gedownload.

## Uitrollen

Kies **Uitrollen** naast een pakket en kies groepen, apparaten of allebei:

- **Groepen bijhouden**: apparaten die later bij de groepen komen, krijgen de
  taak ook, totdat je de uitrol annuleert.
- **Later starten**: de apparaten beginnen vanaf het gekozen tijdstip.
- **Vervalt na**: apparaten die niet begonnen zijn (bijvoorbeeld omdat ze uit
  stonden) worden na dit aantal dagen als *verlopen* gemarkeerd.

Elk apparaat voert één softwaretaak tegelijk uit, en nooit tegelijk met een
patchinstallatie. De agent kijkt eerst of het programma al geïnstalleerd is
(met dezelfde of een nieuwere versie); dan wordt de taak *overgeslagen*.
Anders downloadt hij, installeert stil als SYSTEM of root, en meldt het
resultaat: exitcode, het einde van het installatielog, de versie die hij
daarna vindt en of een herstart nodig is (exitcodes 3010 en 1641). Een taak
die een herstart nodig heeft, herstart het apparaat niet; dat doet het
volgende patchvenster of jijzelf.

**Uitrollen** toont elke uitrol met de voortgang. Open er een om het
resultaat per apparaat en het installatielog te zien, mislukte apparaten
**opnieuw** te proberen of de rest te **annuleren**. Het tabblad **Software**
van een apparaat toont de eigen taken en heeft **Software installeren** voor
één apparaat.

## Verwijderen

Je kunt elk programma uit de inventaris verwijderen, ook programma's die
FleetWarden niet heeft geïnstalleerd:

- **Software > Geïnstalleerde software** toont alle programma's op je
  apparaten. **Verwijderen** selecteert elk apparaat waar het op staat; vink
  apparaten uit of voeg groepen toe als je wilt.
- Op het tabblad **Software** van een apparaat verwijdert het prullenbakje
  een programma van dat apparaat.

Op Windows gebruikt de agent het stille verwijderprogramma van het programma
als dat er is, `msiexec /x {productcode} /qn /norestart` voor MSI-pakketten,
en de standaard stille parameters voor Inno Setup- en NSIS-verwijderprogramma's.
Vul voor andere programma's de stille parameters van de leverancier in bij
*Parameters voor verwijderen*. Op Linux voert hij `apt-get remove` uit (of
`purge` met *Ook configuratiebestanden verwijderen*). De FleetWarden-agent zelf
kan zo niet worden verwijderd.

## Browserupdates

**Software > Browsers** toont de versie van Google Chrome, Microsoft Edge en
Mozilla Firefox op elk Windows-apparaat, naast de nieuwste versie in de
winget-catalogus. **Browsers bijwerken** start een uitrol voor de verouderde
apparaten; je kunt er ook een starten via **Uitrollen** voor groepen (met
*Groepen bijhouden*) of hem plannen met *Later starten*.

Per apparaat doet de agent het volgende:

1. kijkt welke browsers voor de hele computer geïnstalleerd zijn en ouder zijn
   dan de catalogus. Browsers die bijgewerkt of niet aanwezig zijn, worden
   overgeslagen. Firefox houdt zijn editie (ESR) en taal (bijvoorbeeld
   `Mozilla.Firefox.nl`).
2. downloadt de installatiebestanden en controleert hun SHA-256-hash.
3. toont, als een browser open is, een pop-up: *Google Chrome wordt
   bijgewerkt. De browser sluit over 60 seconden en je tabbladen komen daarna
   terug.* De gebruiker kan uitstellen met dezelfde instellingen als het
   patchbeleid van de groep van het apparaat (standaard 3 keer, 60 minuten).
   De uitrol toont *Wacht op gebruiker* zolang hij is uitgesteld.
4. sluit de browsers: Chrome en Edge sluiten normaal met alle vensters tegelijk
   en starten opnieuw met `--restore-last-session`; Firefox wordt beëindigd en
   herstelt zijn sessie bij de volgende start
   (`browser.sessionstore.resume_session_once`).
5. installeert stil, opent de browsers weer voor de gebruiker en meldt de
   versies voor en na.

Browserupdates gebruiken de FleetWarden-tray-app (geïnstalleerd met de agent)
voor de pop-up en om de browsers weer te openen. Zonder aangemelde gebruiker
wordt de update meteen uitgevoerd.

## Rechten

- **Softwarebibliotheek beheren** (`software.manage`, beheerders): pakketten
  toevoegen, wijzigen en verwijderen, bestanden uploaden, de catalogus
  vernieuwen.
- **Software uitrollen en verwijderen** (`software.deploy`, beheerders en
  technici): uitrollen starten, opnieuw proberen en annuleren.
- Gebruikers met alleen leesrechten zien de bibliotheek, de geïnstalleerde
  software en de uitrollen. Gebruikers die beperkt zijn tot apparaatgroepen
  zien en kiezen alleen hun eigen groepen.

## Reverse proxy

Uploads kunnen tot 2 GB groot zijn. Stel de uploadlimiet van je reverse proxy
daarop in, voor nginx `client_max_body_size 2g;` (zie
[reverse-proxy.md](reverse-proxy.md)).
