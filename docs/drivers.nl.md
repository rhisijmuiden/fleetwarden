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

Catalogi van Dell, HP en Lenovo (BIOS en drivers van de fabrikant) en het
uploaden van eigen driverpakketten volgen in een latere update.

## Hoe het werkt

1. Apparaten in een groep met een patchbeleid scannen met de interval onder
   **Instellingen > Agentscans**. Elke scan geeft ook de driver- en
   firmware-updates door. Die staan onder **Drivers & firmware >
   Beschikbaar**, met het aantal apparaten dat ze kan installeren; **Nu
   scannen** op een apparaat scant meteen.
2. Selecteer updates en kies **Toevoegen aan repository**. De server
   downloadt de bestanden bij de leverancier (alleen van de downloadservers
   van Microsoft en LVFS), controleert de checksum van de leverancier als die
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
  verwijderen): beheerders.
- **Nu installeren** op een apparaat: iedereen die apparaten mag beheren.
- De lijsten bekijken: iedereen die apparaten mag zien (beperkt tot hun
  groepen).
