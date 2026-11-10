# Selfservice

Gebruikers van Windows-apparaten kunnen zichzelf helpen via het
FleetWarden-icoon in het systeemvak: klik er met rechts op en kies **Software en
hulp**. Het venster heeft drie onderdelen die je elk aan of uit zet. Engelse
versie: [selfservice.md](selfservice.md).

| Onderdeel | Wat de gebruiker kan |
|---|---|
| **Software** | Programma's installeren die je in de catalogus hebt gezet, met of zonder goedkeuring. |
| **Updates** | De updates die het patchbeleid van de groep heeft goedgekeurd nu installeren, in plaats van te wachten op het onderhoudsvenster. |
| **Hulp** | Een vraag sturen aan IT; die komt binnen met de naam van de gebruiker en de computer. |

De agent weet welke gebruiker het vraagt, via de Windows-sessie waarin de
tray-app draait, dus bij elke aanvraag staat wie hem stuurde.

## Catalogus

Kies onder **Selfservice > Catalogus** voor **Programma toevoegen** en kies een
pakket uit de softwarebibliotheek (*Software*). Per programma stel je in:

- een naam, categorie en omschrijving voor gebruikers (leeg: van het pakket);
- **Zichtbaar voor**: apparaatgroepen, of geen voor alle groepen;
- **Goedkeuring nodig**: een technicus keurt elke aanvraag eerst goed,
  bijvoorbeeld voor programma's met een betaalde licentie. Zonder goedkeuring
  start de installatie meteen;
- **Tonen in de catalogus**: een programma verbergen zonder het te verwijderen.

Installaties lopen als gewone uitrollen, dus je ziet ze onder *Uitrollen* met
"Door de gebruiker (selfservice)" als bron.

## Aanvragen en hulp

- **Installatieaanvragen** toont de aanvragen die op goedkeuring wachten.
  Goedkeuren installeert het programma meteen, of zodra het apparaat online is.
  Bij afwijzen ziet de gebruiker jouw toelichting.
- **Hulpvragen** zijn de vragen van gebruikers. Neem er een op, antwoord (het
  antwoord verschijnt in het venster van de gebruiker), open remote control op
  het apparaat en sluit de vraag als hij klaar is.

## Instellingen

Onder **Selfservice > Instellingen** zet je de drie onderdelen aan of uit voor
alle apparaten, stel je een tekst boven het hulpformulier in (bijvoorbeeld
openingstijden of het nummer van de servicedesk) en vul je tot 20
e-mailadressen in die een mail krijgen bij elke hulpvraag en elke aanvraag die
goedkeuring nodig heeft.

## Tickets vanuit de tray

Als hulpvragen aan staan, wordt het hulpformulier in de tray een
ticketformulier. Onder **Selfservice > Instellingen** kies je:

- **Waar een melding heen gaat**: alleen FleetWarden (Hulpvragen en een e-mail
  aan de ontvangers), een e-mail in de eigen **Outlook** van de gebruiker
  (klaar om te versturen geopend, met het apparaatrapport als HTML-bijlage;
  zonder klassieke Outlook opent het standaard mailprogramma en toont
  Verkenner het rapport om toe te voegen), een e-mail via de **mailserver**
  van FleetWarden, of een **servicedeskkoppeling** (zie
  [koppelingen](integrations.nl.md)). Bij een koppeling kun je terugvallen op
  e-mail als die na alle pogingen faalt. Elke melding blijft ook onder
  Hulpvragen staan, met het ticketnummer, de link en de status van de
  servicedesk (elke 5 minuten bijgewerkt; een ticket dat in de servicedesk
  sluit, sluit de hulpvraag). Een antwoord dat je onder Hulpvragen schrijft,
  komt als interne notitie bij het ticket in de servicedesk.
- **Het formulier**: een verplichte omschrijving met een minimale lengte,
  urgentie (laag, normaal, hoog; de prioriteit in de koppeling), een categorie
  uit jouw lijst, hoe de gebruiker bereikbaar is, en of de gebruiker ziet wat
  er meegaat.
- **Apparaatinformatie**: aangemelde gebruiker (met het e-mailadres als
  Windows dat kent), tijd, IP-adressen, besturingssysteem/domein/groep,
  model/serienummer/CPU/RAM, vrije schijfruimte, uptime, herstart nodig,
  wachtende updates, catalogus- en third-party software (uitrol en verouderde
  programma's, geen ingebouwde Windows-onderdelen), beveiligingsstatus en open
  waarschuwingen. De computernaam gaat altijd mee.
- **Link naar FleetWarden**: een link naar de apparaatpagina, met
  snelkoppelingen (overnemen, terminal, scan). Dit vereist de publieke URL
  onder Instellingen > Algemeen. De link bevat geen token: de medewerker meldt
  zich aan (SSO of lokaal), komt op het apparaat uit met de hulpvraag
  bovenaan, en start de actie met één klik. Rechten en de toestemmingsregels
  van de groep gelden zoals altijd.

**Voorbeeld van het ticket** toont het ticket dat een echt apparaat zou
versturen met de instellingen in het formulier, nog voordat je ze opslaat.

## Chatten met de gebruiker

Op een Windows-apparaat opent **Chatten** (apparaatpagina, of **Chatten met de
gebruiker** in een hulpvraag) rechtsonder een chatpaneel. Het eerste bericht
start de chat: die opent als venster rechtsonder op het scherm van de
aangemelde gebruiker, in de tray-app. De gebruiker kan alleen antwoorden op
een open chat, er nooit zelf een starten. Het paneel laat zien of een tray de
chat toont. Bij snel wisselen van gebruiker ziet iedere aangemelde gebruiker
hem.

**Chat beëindigen** sluit hem voor beide kanten; een chat zonder berichten
stopt na een uur vanzelf. Een chat die vanuit een hulpvraag (of de
ticketlink-actie *Chatten*) is gestart, is daaraan gekoppeld: als hij stopt,
komt het verslag als privénotitie bij het servicedeskticket. Starten en
beëindigen staan in het auditlogboek. Grenzen: 2.000 tekens per bericht en
1.000 berichten per chat. Vereist `selfservice.handle`.

## Rechten

| Recht | Beheerder | Technicus |
|---|---|---|
| Catalogus en instellingen beheren (`selfservice.manage`) | ja | nee |
| Installatieaanvragen goedkeuren en hulpvragen afhandelen (`selfservice.handle`) | ja | ja |

## Grenzen

- Selfservice zit alleen in de Windows-tray-app; Linux-apparaten hebben geen
  tray-venster.
