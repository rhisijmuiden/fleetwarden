# Configuratiebeheer

*English: [configurations.md](configurations.md)*

FleetWarden zet Windows- en Linux-instellingen op apparaten en houdt ze zo,
ook op pc's die niet in een domein zitten. De agent past ze toe, controleert
ze elke 30 minuten opnieuw, zet terug wat iemand heeft veranderd en haalt weg
wat hij heeft ingesteld als een instelling niet meer geldt. Open
**Configuraties** in het menu.

## Configuraties en profielen

Een **configuratie** is één herbruikbaar onderdeel: een browserbeleid, een set
netwerkschijven, firewallregels, enzovoort. Een **profiel** bundelt
configuraties en wordt gekoppeld aan apparaatgroepen. Een apparaat krijgt elke
ingeschakelde configuratie uit de profielen van zijn groepen. Wordt een
apparaat lid van een groep, dan krijgt het meteen de instellingen van het
profiel; verlaat het de groep, dan worden ze weggehaald.

1. **Configuraties > Nieuwe configuratie**: kies het platform en het type,
   vul het in en sla op.
2. **Configuraties > Profielen > Nieuw profiel**: kies de configuraties en de
   apparaatgroepen.

Stellen twee configuraties hetzelfde in, dan wint die met het hoogste
*volgorde*-nummer. Een uitgeschakelde configuratie wordt van de apparaten
verwijderd.

## Typen

| Type | Platform | Wat het doet |
|------|----------|--------------|
| Browserbeleid | Windows, Linux | Startpagina, startknop, wachtwoordmanager, verplichte extensies voor Chrome, Edge en Firefox |
| Register | Windows | Waarden in HKLM, of in HKCU van elke gebruiker die zich aanmeldt |
| Script | Windows, Linux | Draait eenmalig, bij opstarten, bij aanmelden of met een interval, als SYSTEM/root of als de gebruiker |
| Netwerkschijven | Windows | Koppelt schijven zoals `H:` → `\\server\home\%USERNAME%` voor elke gebruiker |
| Printers | Windows | Gedeelde printers en TCP/IP-printers met hun driver, en de standaardprinter |
| Bureaublad | Windows | Achtergrond van elke gebruiker, en de pc vergrendelen na minuten zonder invoer |
| Energie | Windows | Scherm uit, slaapstand en sluimerstand, op netstroom en op de accu |
| Lokale beheerders | Windows | Voegt accounts en groepen toe aan de lokale groep Administrators |
| Firewall | Windows | Windows Firewall-regels op richting, protocol, poorten, adressen en programma |

Opmerkingen per type:

- **Netwerkschijven** gebruiken de eigen Windows-aanmelding van de gebruiker.
  Op een pc zonder domein vraagt Windows de eerste keer om een wachtwoord,
  tenzij de gebruiker hetzelfde account op de server heeft.
- **Printers**: TCP/IP-printers hebben een drivernaam nodig die in Windows
  zit, of een `.inf`-bestand op een share. Gedeelde printers kunnen voor
  iedereen op het apparaat of per gebruiker worden toegevoegd.
- **Bureaublad** en **Energie** worden als Windows-beleid geschreven, op
  dezelfde plek als groepsbeleid: gebruikers kunnen ze niet wijzigen. Een
  nieuwe achtergrond verschijnt bij de volgende aanmelding; energie-instellingen
  gelden meteen.
- **Lokale beheerders**: leden die FleetWarden heeft toegevoegd worden weer
  verwijderd als ze niet meer geconfigureerd zijn. Andere leden blijven. De
  groep wordt gevonden op zijn SID (S-1-5-32-544), dus het werkt in elke
  Windows-taal.
- **Firewall**-regels staan in de Windows Firewall-groep *FleetWarden*.

## Status en groepsbeleid

De configuratiepagina toont per apparaat of de instellingen zijn toegepast,
wachten (bijvoorbeeld tot een gebruiker zich aanmeldt), mislukt of niet
ondersteund zijn, en hoe vaak de agent er een heeft hersteld. **Nu toepassen**
op een apparaat past alles meteen toe. Zit een apparaat in een domein, dan
waarschuwt FleetWarden dat groepsbeleid hetzelfde kan instellen; groepsbeleid
wint waar beide hetzelfde beleid schrijven.
