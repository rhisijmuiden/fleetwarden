# Energiebeheer

*English: [power.md](power.md)*

**Energiebeheer** in het menu wekt uitgeschakelde apparaten met Wake-on-LAN
en herstart of sluit apparaten af, handmatig of volgens een weekschema. Een
patchbeleid kan ook de apparaten wekken die uit staan als het venster opent,
en ze weer afsluiten zodra hun updates zijn geïnstalleerd.

## Wake-on-LAN

Een wekpakket ("magic packet") is een broadcast in het lokale netwerk: het
gaat niet over routers heen en kan niet via internet worden verstuurd.
FleetWarden vraagt daarom maximaal drie **online agents in hetzelfde netwerk**
om het te versturen. Eerst agents achter hetzelfde publieke adres (twee
locaties kunnen dezelfde privé-reeks gebruiken), dan servers (die staan
altijd aan), dan werkplekken. De server verstuurt het pakket zelf ook als
**Ook vanaf de server versturen** aan staat; dat helpt als de server in dat
netwerk staat.

De agent meldt elke netwerkkaart met MAC-adres en netwerk (bijvoorbeeld
`10.20.0.41/24`). Agents ouder dan deze versie melden alleen hun adressen;
FleetWarden gaat dan uit van een /24-netwerk.

Een wekopdracht is gelukt als het apparaat binnen 15 minuten online komt;
anders staat hij in de geschiedenis als mislukt.

**Energiebeheer > Apparaten** laat per uitgeschakeld apparaat zien of het
gewekt kan worden en via welke apparaten. Selecteer daar apparaten om er
meerdere tegelijk te wekken, te herstarten of af te sluiten.

### Wat Wake-on-LAN nodig heeft

- Wake-on-LAN (of "Power on by PCI-E", "Remote wake up") staat aan in het BIOS
  of UEFI. Dat kan FleetWarden niet aanzetten.
- Een ander apparaat met de agent is online in hetzelfde netwerk (VLAN), of de
  server staat in dat netwerk.
- Het apparaat is met een netwerkkabel aangesloten. Wifi-adapters wekken een
  afgesloten computer zelden.

**Energiebeheer > Instellingen > Apparaten voorbereiden op Wake-on-LAN** laat
de agents de rest doen:

- Windows: "wake on magic packet" gaat aan voor de bekabelde fysieke
  netwerkkaarten en Snel opstarten gaat uit. Een computer die met Snel
  opstarten is afgesloten, is half in sluimerstand en veel netwerkkaarten
  wekken hem dan niet. Zet je de instelling uit, dan komt Snel opstarten terug
  zoals het was.
- Linux: `ethtool -s <adapter> wol g` voor de bekabelde netwerkkaarten die
  het ondersteunen (`ethtool` moet geïnstalleerd zijn). De agent past het bij
  elke start opnieuw toe.

De UDP-poort is standaard 9; sommige netwerkkaarten luisteren alleen op 7.

## Herstarten en afsluiten

Op een apparaatpagina voeren **Herstarten** en **Afsluiten** (online
apparaten) en **Wekken** (uitgeschakelde apparaten) direct een actie uit.
Opties:

- **Aangemelde gebruiker waarschuwen**: de tray-melding toont een aftelling;
  de gebruiker kan nu herstarten of afsluiten, of het ingestelde aantal keer
  uitstellen.
- **Alleen als niemand is aangemeld**: het apparaat wordt overgeslagen zolang
  iemand erop werkt.

Windows gebruikt `shutdown /r` of `/s` (volledig afsluiten, niet hybride);
Linux gebruikt `systemctl reboot` of `poweroff`.

Rechten: wekken, herstarten en afsluiten vragen **power.control**
(beheerders en technici); schema's en instellingen vragen **power.manage**
(beheerders).

## Schema's

**Energiebeheer > Schema's** wekt, herstart of sluit de apparaten van een of
meer groepen af op vaste dagen en een vaste tijd, in de tijdzone van de
server of een gekozen tijdzone. Bijvoorbeeld:

- *Werkplekken 's avonds uit*: afsluiten, maandag t/m vrijdag 19:00, gebruiker
  waarschuwen met 2 keer 30 minuten uitstel.
- *Werkplekken 's ochtends aan*: wekken, maandag t/m vrijdag 07:30.
- *Servers herstarten na updates*: herstarten, zondag 03:00, alleen als
  updates op een herstart wachten.

Een wekschema wekt alleen apparaten die uit staan; een herstart- of
afsluitschema bereikt alleen apparaten die op dat moment online zijn. Een
tijdstip dat de server meer dan 30 minuten heeft gemist (omdat hij uit stond)
wordt overgeslagen. **Nu uitvoeren** voert een schema direct uit.

## Patchvensters

Een patchbeleid heeft twee energieschakelaars:

- **Apparaten wekken voor het venster**: als het venster opent, worden de
  apparaten van de groepen die uit staan gewekt (met een tweede poging als de
  eerste niet werkte, zolang er nog minstens een half uur van het venster
  over is).
- **Gewekte apparaten daarna weer afsluiten**: een apparaat dat voor het
  venster is gewekt, wordt weer afgesloten als zijn patchtaak klaar is (na de
  herstart, als die er was), of na een uur online zonder taak, of als het
  venster eindigt. Nooit terwijl iemand is aangemeld, en nooit een apparaat
  dat al aan stond toen het venster opende.

## Energiebeheerschema's

Slaapstand, sluimerstand en schermtime-outs horen bij configuratiebeheer:
maak een configuratie **Windows-instellingen > Energie** en voeg die toe aan
een configuratieprofiel. Zie [configurations.nl.md](configurations.nl.md).

## Geschiedenis

**Energiebeheer > Geschiedenis** toont alle wek-, herstart- en
afsluitopdrachten van de afgelopen 400 dagen: wie of wat ze startte
(handmatig, een schema, het patchvenster, na patchen), welke apparaten het
wekpakket verstuurden, de keren dat de gebruiker uitstelde en de uitkomst.
