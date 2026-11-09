# Kwetsbaarheden (CVE's)

*English: [vulnerabilities.md](vulnerabilities.md)*

FleetWarden koppelt de geïnstalleerde software, de Windows-build en de
ontbrekende updates van elk apparaat aan bekende kwetsbaarheden (CVE's). Per
kwetsbaarheid zie je de ernst, of hij actief wordt misbruikt en hoe je hem
oplost, en je rolt de oplossing vanuit hetzelfde scherm uit. Open
**Kwetsbaarheden** in het menu.

## Waar de gegevens vandaan komen

De server leest één keer per dag vier openbare bronnen. Apparaten maken zelf
nooit contact met die bronnen.

| Bron | Gebruikt voor | Adres |
| --- | --- | --- |
| NVD (NIST) | beschrijvingen, CVSS-score en de getroffen versies van Windows-programma's | `nvd.nist.gov/feeds/json/cve/2.0/` |
| CISA KEV | welke CVE's actief worden misbruikt (label **Misbruikt**) | `www.cisa.gov/.../known_exploited_vulnerabilities.json` |
| Microsoft MSRC | Windows-CVE's, de opgeloste build per Windows-versie en de KB die ze oplost | `api.msrc.microsoft.com/cvrf/v3.0/` |
| Debian security tracker | CVE's per Debian-pakket en de versie die ze oplost (Debian en Proxmox VE) | `security-tracker.debian.org/tracker/data/json` |

De eerste keer leest de server alle NVD-jaren (zo'n 400 MB aan gecomprimeerde
bestanden, een paar minuten). Daarna alleen de NVD-feed met recente
wijzigingen, en eens per week weer de volledige set. Van Microsoft worden
standaard de laatste 12 maanden gelezen (3 tot 36 onder **Bronnen en instellingen**); oudere
maanden worden alleen opnieuw gelezen als Microsoft ze wijzigt. Een bron die
mislukt wordt na twee uur opnieuw geprobeerd; de fout staat onder **Bronnen en instellingen**.

De server moet deze adressen via HTTPS kunnen bereiken. Sta ze toe achter een
proxy of firewall.

Onder **Kwetsbaarheden > Bronnen en instellingen** zet je het scannen op kwetsbaarheden of
losse bronnen uit, wijzig je het aantal Microsoft-maanden en lees je de
bronnen direct opnieuw met **Nu bijwerken**.

## Hoe apparaten worden gekoppeld

Het koppelen gebeurt op de server, op basis van wat de agents al rapporteren;
op de apparaten draait niets extra. Een apparaat wordt opnieuw gecontroleerd
na elke inventarisatie en updatescan, en alle apparaten na elke update van de
bronnen.

- **Windows zelf**: de Windows-versie en build (bijvoorbeeld Windows 11 24H2,
  build 26100.2314) worden vergeleken met de opgeloste build die Microsoft per
  CVE noemt. De oplossing is de ontbrekende cumulatieve update die hem bevat.
- **Ontbrekende Windows-updates**: elke ontbrekende update waarvan Microsoft
  de KB als oplossing voor een CVE noemt (bijvoorbeeld een .NET- of
  Office-update) wordt aan die CVE's gekoppeld.
- **Windows-programma's**: een lijst met veelgebruikte programma's wordt
  vergeleken met de getroffen versies in NVD: Chrome, Edge, Firefox, Firefox
  ESR, Thunderbird, 7-Zip, VLC, Notepad++, Adobe Acrobat (Reader), PuTTY,
  WinSCP, Wireshark, Git, FileZilla, KeePass, TeamViewer, AnyDesk, Visual
  Studio Code, LibreOffice, Foxit PDF Reader, Node.js, VirtualBox, VMware
  Workstation, WinRAR, Slack, Docker Desktop, PowerShell, Zoom, Greenshot en
  Python. De volledige lijst staat onder **Bronnen en instellingen**. Andere programma's
  worden niet gekoppeld: NVD noemt producten anders dan Windows ze toont, en
  gokken geeft vals alarm.
- **Debian en Proxmox VE** (bullseye, bookworm, trixie): elk geïnstalleerd
  pakket wordt via het bronpakket en de versie vergeleken met de Debian
  security tracker. CVE's die Debian als onbelangrijk markeert, of waarvoor
  nog geen oplossing bekend is, worden overgeslagen. De agent rapporteert het
  bronpakket vanaf deze versie; apparaten met een oudere agent worden
  gekoppeld zodra ze zijn bijgewerkt.

Ubuntu, andere Linux-distributies en macOS worden nog niet gekoppeld; het
tabblad van het apparaat meldt dat.

## Ernst en oplossing

De ernst volgt uit de CVSS-score (NVD, of Microsoft voor Windows-CVE's):
**Kritiek** (9,0 en hoger), **Hoog** (7,0), **Gemiddeld** (4,0), **Laag** en
**Geen score**. CVE's uit de CISA-lijst krijgen het label **Misbruikt**; pak die als
eerste aan.

Elke bevinding heeft een oplossing:

| Oplossing | Betekenis | Uitrollen met |
| --- | --- | --- |
| **Update beschikbaar** | een ontbrekende Windows- of Debian-update die het apparaat aangeboden krijgt lost hem op | een patchtaak op het apparaat |
| **Nieuwe versie beschikbaar** | een nieuwere versie van het programma lost hem op | een software-uitrol via winget (of de browserupdate) |
| **Opgelost, nog niet aangeboden** | er is een opgeloste versie, maar het apparaat krijgt die nog niet aangeboden (bijvoorbeeld een Windows-versie die geen updates meer krijgt, of een Debian-oplossing die nog niet in de repositories van het apparaat staat) | – |
| **Nog geen oplossing** | nog geen oplossing bekend | – |

## Een oplossing uitrollen

Selecteer CVE's in de lijst, op de CVE-pagina of op het tabblad
**Kwetsbaarheden** van een apparaat, en kies **Oplossing uitrollen**. Je hebt
het recht **Apparaten beheren** nodig.

- Patches worden direct geïnstalleerd op de online apparaten met een
  handmatige patchtaak, met het patchbeleid van het apparaat
  (herstartinstellingen; onderhoudsvenster en update-ringen gelden niet voor
  een handmatige taak). Apparaten waarop nog een patchtaak loopt en
  Proxmox-clusternodes terwijl een andere node patcht worden overgeslagen,
  net als offline apparaten: die krijgen de update zoals gewoonlijk in hun
  volgende onderhoudsvenster. Het resultaat noemt ze.
- Programma's worden bijgewerkt met een software-uitrol naar de getroffen
  apparaten. Het winget-pakket wordt eerst aan de softwarebibliotheek
  toegevoegd als het er nog niet in staat (met de naam *Vulnerability fix:
  ...*). Browsers gebruiken de browserupdate-actie. Apparaten pakken de uitrol
  op bij hun volgende check-in, ook als ze offline waren.

Het resultaat toont per apparaat wat is gestart en wat is overgeslagen, met de
reden. Elke uitrol komt in het auditlog.

## Een risico accepteren

Geldt een kwetsbaarheid niet, of is hij nog niet op te lossen, accepteer het
risico dan met de schildknop: voor één apparaat of voor alle apparaten, met
een reden en eventueel een einddatum. Geaccepteerde CVE's tellen niet meer
mee in de totalen, het dashboard en de apparatenlijst; **Geaccepteerde risico's tonen**
laat ze weer zien. Accepteren vereist het recht **Kwetsbaarheidsbronnen en geaccepteerde risico's beheren**
(beheerders), en voor alle apparaten toegang tot alle apparaten.
Geaccepteerde risico's staan onder **Kwetsbaarheden > Geaccepteerde risico's** en
verdwijnen vanzelf als ze verlopen.

## Back-up en herstel

De kwetsbaarheidsbronnen zitten niet in de back-up: ze zijn openbaar en groot.
Na een herstel leest de server ze opnieuw in; daarna worden de apparaten
opnieuw gekoppeld. Bevindingen, geaccepteerde risico's en de instellingen
zitten wel in de back-up.
