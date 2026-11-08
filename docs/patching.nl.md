# Patchmanagement

*English: [patching.md](patching.md)*

FleetWarden installeert Windows-updates en apt-updates (Debian, Ubuntu,
Proxmox VE) in onderhoudsvensters die je per apparaatgroep instelt. Open
**Patchbeheer** in het menu.

## Zo werkt het

1. Maak een **patchbeleid** onder **Patchbeheer > Beleid** en koppel het aan
   een of meer groepen onder **Groepen** (groep bewerken, *Patchbeleid*).
   Apparaten in een groep zonder beleid worden niet automatisch gepatcht.
2. Online agents scannen op ontbrekende updates volgens het interval onder
   **Instellingen > Agentscans** (standaard elke 6 uur; een groep kan een
   eigen interval hebben), en direct na een taak. **Nu scannen** op het tabblad **Updates**
   van een apparaat scant meteen.
3. Als het venster opengaat, stuurt de server elk online apparaat één taak met
   de goedgekeurde updates. Apparaten die offline zijn krijgen hun updates in
   het volgende venster. Een venster mag over middernacht lopen en gebruikt de
   tijdzone van het beleid (of die van de server).
4. De agent vraagt eerst de ingelogde gebruiker als het beleid dat wil,
   installeert, en herstart volgens de herstartinstelling. De taak en het
   resultaat per update staan in de patchgeschiedenis van het apparaat en
   onder **Patchbeheer > Overzicht**.

## Goedkeuring

Elke update valt in een categorie: security, kritiek, cumulatief, definities,
functie-updates of overig. Drivers en firmware zijn geen patches: die lopen
via je eigen repository, zie [drivers.nl.md](drivers.nl.md). Op Linux is een update *security* als hij
uit een security-suite komt (bijvoorbeeld `trixie-security`); de rest is
*overig*.

Per categorie zegt het beleid:

- **Automatisch goedkeuren**, eventueel pas na een aantal dagen (vanaf de
  releasedatum, of vanaf het moment dat FleetWarden de update voor het eerst
  zag). Standaard wacht cumulatief 7 dagen en overig 3 dagen.
- **Handmatig goedkeuren**: pas geïnstalleerd nadat je hem goedkeurt.
- **Nooit installeren** (standaard voor functie-updates zoals een nieuwe
  Windows-versie).

Onder **Patchbeheer > Updates** zie je alle ontbrekende updates van je
apparaten. **Goedkeuren** of **Weigeren** geldt voor alle apparaten en gaat
altijd voor de categorieregel; **Volg beleid** haalt het besluit weg. Op het
tabblad **Updates** van een apparaat kun je ook updates selecteren en direct
installeren (buiten het venster), bijvoorbeeld een fix voor één machine.

## Drivers en firmware

Een beleid zegt ook of apparaten in het venster de drivers en firmware uit de
[driver-repository](drivers.nl.md) installeren (**Drivers uit de repository
installeren**, standaard aan, en **Firmware (BIOS/UEFI) uit de repository
installeren**, standaard uit). Ze gebruiken hetzelfde venster, dezelfde pop-up
en dezelfde herstartinstellingen als de andere updates, in dezelfde taak.

## Herstarten en de gebruiker

- **Gebruiker vragen, daarna herstarten**: de gebruiker mag de herstart
  uitstellen (maximaal *Aantal keer uitstellen*, telkens met *Uitstellen met*
  minuten); na de laatste keer herstart het apparaat toch.
- **Automatisch herstarten**: herstart direct na het installeren; een
  ingelogde Windows-gebruiker ziet eerst de pop-up met een aftelling.
- **Nooit herstarten**: het apparaat meldt *Herstart nodig* en je herstart het
  zelf.

Met **Eerst de ingelogde gebruiker vragen** toont Windows vóór het installeren
een pop-up rechtsonder, met dezelfde regels voor uitstellen. Bij een herstart
ziet een ingelogde gebruiker de pop-up altijd, ook als dit uit staat. Zonder
antwoord binnen de aftelling gaat de taak door. Zonder ingelogde gebruiker
(servers) start de taak meteen. De pop-up gebruikt je huisstijl (productnaam
en kleur) en de Windows-taal van de gebruiker (Engels of Nederlands).

De pop-up komt van de FleetWarden-trayapp (`FleetWardenTray.exe`), die de MSI
naast de agent installeert. Die start bij het inloggen, en de agent start hem
ook in open sessies zodra hij iets te vragen heeft. Er is geen extra runtime
nodig (.NET Framework 4.7.2 zit in Windows 10 en 11) en hij praat alleen via
een lokale named pipe met de agentservice.

## Windows Update

Met **Windows Update overnemen** aan (standaard) zet de agent de eigen
automatische installaties van Windows uit, zodat updates alleen in jouw
vensters komen. Zet je het uit, of haal je het beleid weg, dan krijgt Windows
de controle terug.

Krijgen apparaten ook updates via Intune (Windows Update for Business) of een
WSUS-server via GPO, kies dan één tool die de baas is: twee tools die allebei
updates installeren zitten elkaar in de weg. FleetWarden gebruikt de
updatebron waarvoor Windows is ingesteld (Microsoft Update of WSUS) en toont
die bij het apparaat.

## Debian, Ubuntu en Proxmox VE

De agent draait `apt-get update` en simuleert `apt-get dist-upgrade` om
ontbrekende updates te vinden, zodat de lijst klopt met wat Proxmox aanraadt
(nooit een kale `apt upgrade`). Een taak werkt precies de goedgekeurde
pakketten bij; apt haalt nieuwe afhankelijkheden zelf op. De eigen pakketten
van FleetWarden laat hij over aan de agent- en serverupdater. Een herstart is
nodig als `/run/reboot-required` bestaat of als er een nieuwere kernel
geïnstalleerd is dan de draaiende.

**Proxmox VE-clusters** worden node voor node gepatcht: de volgende node van
hetzelfde cluster start pas als de taak van de vorige node (inclusief de
herstart) klaar is. Geef Proxmox-nodes een eigen groep en beleid, bijvoorbeeld
met een nachtvenster en *Automatisch herstarten*, en zorg dat HA of een
migratieplan de VM's opvangt tijdens de herstart.

## Rechten

- **Patchbeleid beheren**: beheerders.
- **Updates goedkeuren of weigeren**: beheerders en technici.
- **Nu scannen / Nu installeren** op een apparaat: iedereen die apparaten mag
  beheren.
