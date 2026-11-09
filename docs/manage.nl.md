# Beheer zonder het scherm over te nemen

*English: [manage.md](manage.md)*

Het tabblad **Beheer** op een apparaatpagina beheert een Windows- of
Linux-apparaat terwijl de gebruiker gewoon doorwerkt: niets hiervan verschijnt
op het scherm of vraagt de gebruiker iets. De agent doet het werk als SYSTEM
(Windows) of root (Linux).

Het tabblad vraagt het recht **Apparaten beheren zonder overname**
(`remote.manage`). De rollen Beheerder en Technicus hebben het; gebruikers met
alleen-lezen zien het tabblad niet. Groepsafbakening geldt zoals overal: wie
maar een paar groepen ziet, kan alleen die apparaten beheren. Het apparaat moet
online zijn.

Elke wijziging (een proces beëindigen, een service-actie, een bestand maken,
hernoemen, verwijderen, downloaden of uploaden) komt in het auditlogboek, met
het apparaat, het pad of de naam en de gebruiker die het deed.

## Processen

De draaiende programma's met PID, gebruiker (met de Windows-sessie),
CPU-gebruik gemeten over een halve seconde, geheugen (working set / RSS),
starttijd en opdrachtregel. Sorteer op elke kolom, zoek op naam, PID,
gebruiker of opdrachtregel, en zet **Elke 5 s vernieuwen** aan om een
programma te volgen.

**Beëindigen** stopt een proces (Windows: `TerminateProcess`; Linux:
`SIGTERM`, en na vijf seconden `SIGKILL`). Processen waar Windows niet zonder
kan (System, smss, csrss, wininit, winlogon, services, lsass), init en
kernelthreads op Linux, en de agent zelf zijn beschermd en kunnen niet worden
beëindigd.

## Services

- **Windows**: alle services met status, opstarttype en account. Start, stop
  en herstart een service en wijzig het opstarttype (automatisch, handmatig,
  uitgeschakeld). Een service stoppen waar andere van afhangen mislukt met een
  duidelijke melding; stop die eerst.
- **Linux**: alle systemd-services. Starten, stoppen en herstarten;
  **Automatisch** is `systemctl enable`, **Handmatig** is `systemctl disable`,
  **Uitgeschakeld** is `systemctl mask`. Statische units hebben geen
  opstarttype om te wijzigen.

De eigen service van de agent staat in de lijst maar kan hier niet worden
gewijzigd. Stoppen, herstarten of uitschakelen vraagt eerst om bevestiging.

## Logboeken

- **Windows**: Toepassing, Systeem, Beveiliging en Installatie, plus de
  operationele logboeken van PowerShell, Windows Defender, Taakplanner,
  Windows Update, Extern bureaublad-sessies en BITS als de machine die heeft.
  Berichten worden opgemaakt door de eigen bron van de gebeurtenis, in de taal
  van het apparaat.
- **Linux**: het systemd-journaal (alles, of alleen kernelberichten).

Filter op niveau (kritiek, fouten, waarschuwingen, informatie of alles),
periode, bron (Windows-bron, of een systemd-unit of programma op Linux),
gebeurtenis-ID en tekst. De nieuwste gebeurtenissen staan bovenaan, tot 2.000
per zoekopdracht; klik op een gebeurtenis voor de volledige tekst. **CSV
exporteren** slaat de lijst op.

## Bestanden

Blader door de stations (Windows) of het bestandssysteem vanaf `/` (Linux), of
typ een pad. Je kunt mappen maken, bestanden en mappen hernoemen en
verwijderen (een map met alles erin, na bevestiging). Stations zelf kunnen niet
worden verwijderd.

- **Downloaden**: een bestand, of een hele map als zip-bestand (bestanden die
  in gebruik of niet leesbaar zijn worden overgeslagen). Grote bestanden zijn
  geen probleem: de agent stuurt ze in blokken van 8 MB naar de server, dus ze
  gaan ook door de secure gateway.
- **Uploaden**: een of meer bestanden naar de geopende map, met de knop of door
  ze op de lijst te slepen. Bestaande bestanden worden alleen vervangen als je
  dat bevestigt. De agent controleert de SHA-256 voordat het bestand op zijn
  plek komt.

De grens is 4 GB per bestand. Overdrachten lopen op de achtergrond en staan
onder **Overdrachten** met hun voortgang; een afgeronde download blijft een uur
beschikbaar (alleen voor de gebruiker die hem startte) en wordt daarna van de
server verwijderd. Overdrachten staan in `DataDir/transfers`, dat niet in de
back-up zit, en overleven een herstart van de server niet.

## Console

De PowerShell- (Windows) of bash-console (Linux) hoort bij
[overnemen op afstand](remote.nl.md): die opent een terminal in een
browsertabblad, ook zonder het scherm over te nemen.

## Hoe het werkt

De server stuurt een ondertekend verzoek over de bestaande verbinding van de
agent en wacht op het ondertekende antwoord; behalve het auditlogboek wordt
niets op de server opgeslagen. Bestandsinhoud gaat niet over die verbinding:
de agent uploadt of downloadt die met verzoeken die met zijn apparaatsleutel
zijn ondertekend, en alleen voor een overdracht die de server voor dat
apparaat heeft geopend.
