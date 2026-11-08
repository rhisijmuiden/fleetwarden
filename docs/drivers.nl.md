# Drivers en firmware

*English: [drivers.md](drivers.md)*

FleetWarden heeft een eigen repository met driver- en firmware-updates.
Apparaten melden welke updates ze kunnen krijgen, maar er wordt niets
gedownload tot een beheerder een update aan de repository toevoegt. De server
downloadt hem dan één keer bij de leverancier en apparaten installeren hem
vanaf de server, nooit vanaf internet. Open **Drivers & firmware** in het menu.

## Waar updates vandaan komen

- **Windows**: de driver- en firmware-updates die Windows Update voor de
  hardware aanbiedt (beeldscherm, netwerk, chipset, Surface en andere
  UEFI-firmware). De agent vraagt dit bij elke updatescan aan Windows Update,
  maar downloadt niets.
- **Linux** (Debian, Ubuntu, Proxmox VE): firmware van de Linux Vendor
  Firmware Service via `fwupd`, als die geïnstalleerd is
  (`apt install fwupd`). fwupd haalt alleen de lijst met updates op; de
  firmware zelf komt uit de repository.

- **BIOS van Dell, HP en Lenovo**: de server leest de BIOS-catalogi van de
  fabrikant voor de modellen onder je Windows-apparaten (zie hieronder).
- **Eigen pakketten**: drivers en firmware die je zelf uploadt (zie
  hieronder).

## BIOS uit de fabrikantcatalogi

Eén keer per dag, en meteen voor een model dat hij nog niet kende, leest de
server de BIOS-catalogus van elk Dell-, HP- en Lenovo-model onder de
Windows-apparaten. Alleen de catalogusbestanden worden gelezen; de nieuwste
BIOS die een apparaat kan krijgen staat onder **Beschikbaar** zoals elke
andere update, en wordt pas gedownload als je hem aan de repository
toevoegt. De download wordt gecontroleerd met de SHA-256 van de fabrikant.

| Fabrikant | Gekoppeld op | Catalogus | Geïnstalleerd met |
| --- | --- | --- | --- |
| Dell | Systeem-ID (SKU) en BIOS-versie | `downloads.dell.com/catalog/CatalogIndexPC.cab` | het Dell Update Package, `/s` |
| HP | Platform-ID (moederbord), ROM-familie en versie | referentiebestand van HP Image Assistant (`hpia.hpcloud.hp.com`) | `HpFirmwareUpdRec.exe -b -s` uit de SoftPaq |
| Lenovo | Machinetype en BIOS-niveau | `download.lenovo.com/catalog/<type>_Win11.xml` | `winuptp.exe -s` uit het pakket |

De agent geeft het systeem-ID, het moederbord en de kale BIOS-versie door
met zijn inventaris; apparaten met een oudere agent worden gekoppeld zodra ze
zijn bijgewerkt. De server heeft `cabextract` nodig om de catalogi van Dell
en HP uit te pakken (een afhankelijkheid van het pakket). Onder **Drivers &
firmware > Fabrikantcatalogi** zet je elke fabrikant uit, lees je de catalogi
meteen opnieuw en zie je welke modellen er zijn gevonden. BIOS-updates van de
fabrikant zijn firmware: ze worden alleen in het patchvenster geïnstalleerd
als het beleid firmware toestaat, BitLocker wordt voor één herstart
opgeschort en het hulpmiddel van de fabrikant herstart niet zelf; de
patchtaak herstart volgens de herstartinstellingen van het beleid. Een BIOS
met een setup-wachtwoord kan Dell niet stil bijwerken; de taak meldt dat dan.

Drivers van de fabrikant uit deze catalogi worden niet aangeboden: Windows
Update heeft dezelfde drivers voor deze modellen, en koppelen zou de PCI-ID's
van elk onderdeel vragen. Upload zelf een driver van de fabrikant als je een
specifieke nodig hebt.

## Eigen pakketten

Onder **Drivers & firmware > Eigen pakketten** slaat **Pakket uploaden** een
pakket meteen op de server op (maximaal 2 GB; sta achter een reverse proxy
uploads van die grootte toe, zie [reverse-proxy.md](reverse-proxy.md)):

- **Een driver voor Windows**: een `.zip` of `.cab` met de `.inf` van de
  driver (en de `.sys` en `.cat`). De agent pakt hem uit en installeert elke
  `.inf` met `pnputil /add-driver /subdirs /install`.
- **Een programma voor Windows** (`.exe`), zoals het driver- of
  BIOS-installatieprogramma van een fabrikant, met de argumenten voor een
  stille installatie (bijvoorbeeld `/s`). Het draait als SYSTEM; exitcodes 0,
  3010 en 1641 betekenen geslaagd.
- **Een firmwarecabinet voor Linux** (`.cab` voor fwupd), geïnstalleerd met
  `fwupdmgr install`.

Kies de apparaten met **Fabrikant bevat** en **Model bevat** (vergeleken met
de inventaris, hoofdletters maken niet uit; leeg past op elk apparaat van het
platform). Het pakket wordt aan die apparaten aangeboden en geïnstalleerd als
een repository-update: in het patchvenster (drivers of firmware, volgens het
beleid) of met **Nu installeren** op een apparaat. Geüploade pakketten zitten
in de back-up, omdat ze niet opnieuw te downloaden zijn; ze staan onder
`/var/lib/fleetwarden/driver-uploads`.

## Hoe het werkt

1. Apparaten in een groep met een patchbeleid scannen met de interval onder
   **Instellingen > Agentscans**. Elke scan geeft ook de driver- en
   firmware-updates door. Die staan onder **Drivers & firmware >
   Beschikbaar**, met het aantal apparaten dat ze kan installeren; **Nu
   scannen** op een apparaat scant meteen.
2. Selecteer updates en kies **Toevoegen aan repository**. De server
   downloadt de bestanden bij de leverancier (alleen van de downloadservers
   van Microsoft, LVFS, Dell, HP en Lenovo), controleert de checksum van de leverancier als die
   er is, en slaat ze op in `/var/lib/fleetwarden/repository`. Een mislukte
   download wordt drie keer geprobeerd; **Opnieuw proberen** begint opnieuw.
   **Afwijzen** verbergt een update die je niet wilt.
3. In het patchvenster installeren apparaten waarvan het beleid **Drivers uit
   de repository installeren** (standaard aan) of **Firmware (BIOS/UEFI) uit
   de repository installeren** (standaard uit) heeft de repository-updates
   die ze nodig hebben, in dezelfde taak als de andere updates en met dezelfde
   pop-up en herstartinstellingen. Op het tabblad **Drivers** van een apparaat
   kun je repository-updates ook meteen installeren.
4. **Uit repository verwijderen** wist de bestanden; de update gaat terug naar
   de lijst met beschikbare updates.

## Op het apparaat

- **Windows**: de agent haalt het cab-bestand op bij de server, pakt het uit
  en installeert de driver met `pnputil /add-driver /install`, dat de
  passende apparaten bijwerkt. Met **Drivers uit de repository installeren**
  aan haalt Windows niet meer zelf drivers op als er een apparaat wordt
  aangesloten (de agent zet *Instellingen voor apparaatinstallatie* op *Nee*
  en zet dat terug als de instelling uit gaat). Gebruik **Windows Update
  overnemen** zodat Windows Update ook niet zelf drivers installeert.
- **Firmware op Windows** wordt klaargezet en door Windows tijdens de
  volgende herstart toegepast. De agent onderbreekt BitLocker voor die ene
  herstart, zodat het apparaat niet om de herstelsleutel vraagt.
- **BIOS van Dell, HP en Lenovo**: de agent draait het hulpmiddel van de
  fabrikant stil (zie de tabel hierboven), na het uitpakken van de SoftPaq of
  het Lenovo-pakket.
- **Linux**: de agent draait `fwupdmgr install` met het cab-bestand van de
  server op het apparaat waarvoor het werd aangeboden. UEFI-firmware wordt
  tijdens de volgende herstart toegepast.

Firmware-updates kunnen een apparaat onbruikbaar maken als het misgaat
(stroomuitval tijdens de update). Probeer ze eerst op een paar apparaten,
houd laptops aan de stroom en zet firmware alleen aan voor groepen waar je
het wilt.

## Schijfruimte en back-ups

De repository staat op de dataschijf van de server; de pagina toont de
grootte en de vrije ruimte. Een download stopt als er minder dan 2 GB over
zou blijven. De repository zit niet in de back-up, omdat alles opnieuw
gedownload kan worden: na een restore downloadt de server de
repository-updates vanzelf opnieuw.

De bestanden worden opgeslagen onder hun SHA-256 en apparaten halen ze op
met die checksum. Een aparte repository- of cachingserver (gepland na versie
1.0) kan dan dezelfde bestanden dicht bij de apparaten aanbieden.

## Rechten

- **Drivers en firmware aan de repository toevoegen** (toevoegen, afwijzen,
  verwijderen, pakketten uploaden, instellingen van de fabrikantcatalogi):
  beheerders.
- **Nu installeren** op een apparaat: iedereen die apparaten mag beheren.
- De lijsten bekijken: iedereen die apparaten mag zien (beperkt tot hun
  groepen).
