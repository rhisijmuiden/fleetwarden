# Assetbeheer

*English: [assets.md](assets.md)*

**Assets** in het menu laat zien wie welk apparaat heeft, waar het staat en
tot wanneer de garantie loopt, en op een tweede tabblad hoeveel
softwarelicenties je hebt gekocht tegenover hoeveel er in gebruik zijn.

## Apparaten

Elk aangemeld apparaat staat in de lijst met zijn hardware (fabrikant, model
en serienummer, zoals de agent ze meldt) en de gegevens die jij toevoegt:

| Veld | Betekenis |
|---|---|
| Eigenaar | Naam of e-mailadres. Is er niemand ingevuld, dan toont de lijst de aangemelde gebruiker in grijs; het bewerkformulier heeft een knop om die over te nemen. |
| Inventarisnummer | Je eigen inventaris- of assetnummer. |
| Locatie, afdeling | Vrije tekst, bijvoorbeeld *Utrecht, 2e verdieping* en *Financiën*. |
| Leverancier, ordernummer | Waar en onder welke order het apparaat is gekocht. |
| Aangeschaft, garantie tot | Datums. De garantiedatum bepaalt de statussen hieronder. |

De tegels bovenaan tellen de apparaten waarvan de garantie **verlopen** is,
**binnenkort afloopt**, **geldig** is of **onbekend** is; klik op een tegel om
de lijst te filteren. *Binnenkort* betekent binnen het aantal dagen onder
**Rapporten > Instellingen > Loopt binnenkort af** (standaard 90). Zoeken
vindt hostnamen, serienummers, modellen, eigenaren, locaties,
inventarisnummers en leveranciers.

Bewerk één apparaat met het potlood, of op de apparaatpagina in de kaart
**Assetgegevens**. Om er veel tegelijk te wijzigen vink je de apparaten aan en
kies je **Assetgegevens bewerken**: alleen de velden die je aanvinkt
veranderen.

### CSV importeren en exporteren

**CSV exporteren** downloadt het register. **CSV importeren** leest een
bestand, bijvoorbeeld de assetlijst van je leverancier:

- Apparaten worden eerst op serienummer gezocht en daarna op hostnaam (de
  korte naam past ook op `pc-01.corp.example`). Regels die bij geen of bij
  meerdere apparaten passen worden getoond en overgeslagen.
- Herkende kopjes, in het Nederlands of Engels: hostnaam, serienummer,
  inventarisnummer, eigenaar, locatie, afdeling, leverancier, ordernummer,
  aankoopdatum en garantie tot (ook *Service Tag*, *Serial number*,
  *Warranty end*, *Owner*, ...). Andere kolommen worden genegeerd.
- Lege cellen houden de huidige waarde; een los streepje (`-`) maakt het veld
  leeg.
- Datums als `JJJJ-MM-DD`, of met de dag eerst als `DD-MM-JJJJ`,
  `DD/MM/JJJJ` of `DD.MM.JJJJ`.
- Komma's, puntkomma's en tabs werken allemaal als scheidingsteken.

De import laat eerst zien wat er zou veranderen; er wordt niets opgeslagen
voordat je bevestigt. Een geëxporteerd bestand importeert weer zonder
wijzigingen.

## Softwarelicenties

Een licentie heeft een naam, het **aantal licenties** dat je hebt gekocht,
een optionele einddatum (leeg is eeuwigdurend), kosten, notities en een
optionele licentiesleutel. **Programmanaam** bepaalt welke geïnstalleerde
programma's een licentie gebruiken, zoals ze op een apparaat onder *Software*
staan; `*` is een jokerteken, bijvoorbeeld `Microsoft 365 Apps*`. Een
optionele **uitgever** maakt het nauwkeuriger. Tijdens het typen toont het
formulier op hoeveel apparaten het gevonden wordt en welke programmanamen het
meetelt.

**In gebruik** is het aantal apparaten met een passend programma, over alle
apparaten. Een licentie is **overschreden** als meer apparaten het programma
hebben dan er licenties zijn, en **loopt binnenkort af** binnen hetzelfde
aantal dagen als garanties. Het oog-icoon toont de apparaten die de licentie
gebruiken; gebruikers die maar een deel van de groepen zien, zien alleen die
apparaten.

Licentiesleutels worden versleuteld opgeslagen met de geheime sleutel van de
server. Alleen gebruikers met het recht *Assets en softwarelicenties beheren*
kunnen een sleutel tonen, en elke keer wordt dat in het auditlogboek
vastgelegd.

## Meldingen en rapporten

- **Meldingen**: nieuwe installaties hebben de regel *Warranty ending* (60
  dagen voor het einde, zonder ontvangers). Voeg ontvangers toe onder
  **Meldingen > Meldingsregels**, of maak een eigen regel van het soort *De
  garantie afloopt*. Een melding blijft open zolang de garantie verlopen is
  en sluit als er een latere datum wordt ingevuld. Zie
  [alerts.nl.md](alerts.nl.md).
- **Rapporten > Assets** toont overschreden en aflopende licenties en de
  apparaten waarvan de garantie verlopen is of binnenkort afloopt. Als PDF
  heeft het dezelfde tabellen; de CSV is het volledige assetregister. Het kan
  volgens planning gemaild worden zoals de andere rapporten, zie
  [reports.nl.md](reports.nl.md).

## Rechten

Iedereen die apparaten ziet, ziet het assetregister en de licenties.
Beheerders en technici mogen ze wijzigen (recht `assets.manage`);
alleen-lezen-gebruikers niet.

## Niet inbegrepen

Garantiedatums worden niet automatisch bij Dell, HP of Lenovo opgehaald: hun
garantiediensten vragen per organisatie een aparte API-overeenkomst. Importeer
in plaats daarvan de assetlijst die je leverancier stuurt.
