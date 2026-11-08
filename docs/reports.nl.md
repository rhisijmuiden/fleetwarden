# Rapporten

*English: [reports.md](reports.md)*

Open **Rapporten** in het menu voor een overzicht van de hele vloot of van
één groep. Het dashboard toont de belangrijkste cijfers ook als tegels.
Iedereen die apparaten mag bekijken kan de rapporten openen en ziet alleen
de groepen waar hij toegang toe heeft. Geplande rapporten en de
rapportinstellingen vragen het recht *Geplande rapporten en
compliance-regels beheren* (standaard Beheerders).

## Compliance

Een apparaat is **compliant** als dit allemaal klopt:

- het is de afgelopen 7 dagen gezien;
- het heeft een patchscan;
- het mist geen beveiligings- of kritieke update die langer dan 14 dagen
  geleden uitkwam (geweigerde updates tellen niet mee);
- het heeft geen verouderde software;
- elke schijf van 2 GiB of groter heeft minstens 10% vrije ruimte.

De getallen (7 dagen, 14 dagen, 10%) pas je aan onder
**Rapporten > Instellingen**. Het tabblad Compliance toont het percentage
per groep en elk apparaat dat niet compliant is, met de redenen.

## Ontbrekende patches

De updates die apparaten volgens hun laatste patchscan missen, met de
categorie, de uitgiftedatum en het aantal apparaten, en daaronder de
apparaten die aandacht vragen. Geweigerde updates blijven weg. Keur ze goed
of installeer ze via **Patchbeheer**.

## Verouderde software

FleetWarden vergelijkt de programma's op Windows-apparaten met de
winget-catalogus (zie [software.nl.md](software.nl.md)). Een programma is
verouderd als de catalogus een nieuwere versie heeft. De koppeling gebruikt
de naam en uitgever uit *Programma's en onderdelen*, zoals `winget upgrade`
dat doet; een programma dat niet in de catalogus staat of niet zeker te
koppelen is, blijft weg. Store-apps blijven ook weg. Verouderde
Linux-pakketten staan bij *Ontbrekende patches*.

- **Bijwerken** opent een uitrol van de nieuwste versie voor de apparaten
  met een oude versie. Staat het pakket nog niet in de softwarebibliotheek, dan wordt
  het eerst toegevoegd (daarvoor is *Softwarebibliotheek beheren* nodig).
- **Negeren** haalt een programma uit het rapport en de compliance-check,
  bijvoorbeeld als je bewust een oude versie houdt. Terugdraaien kan onder
  **Rapporten > Instellingen**.

## Exporteren

**PDF** en **CSV** boven aan de pagina exporteren het open tabblad voor de
gekozen groep, in de taal van de interface. De PDF krijgt het logo, de kleur
en de productnaam uit **Huisstijl**. De CSV is UTF-8 en opent direct in
Excel: in het Nederlands met een puntkomma, in het Engels met een komma.
Elke export komt in het auditlog.

## Geplande rapporten

**Rapporten > Geplande rapporten > Nieuw gepland rapport** mailt een of meer
rapporten naar maximaal 20 adressen:

- dagelijks, wekelijks (op een gekozen dag) of maandelijks (op dag 1 tot
  28), op een gekozen tijd in een gekozen tijdzone;
- alle groepen of een selectie;
- als één PDF met alle gekozen rapporten en/of een CSV per rapport;
- in het Engels of Nederlands.

De mail zelf vat de belangrijkste cijfers samen. Hij gaat via de mailserver
onder **Administratie > Mailserver**, die aan moet staan. **Nu versturen**
verstuurt een planning meteen zonder de volgende keer te verschuiven; de
lijst toont wanneer hij het laatst liep en eventueel de fout.

De PDF wordt door de server zelf gemaakt (er is geen browser of externe
dienst nodig) met het lettertype Inter, dat is meegeleverd onder de SIL Open
Font License (`internal/reports/fonts/OFL.txt`).

## API

Zie de tabel in [reports.md](reports.md#api).
