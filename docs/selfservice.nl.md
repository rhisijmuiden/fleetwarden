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

## Rechten

| Recht | Beheerder | Technicus |
|---|---|---|
| Catalogus en instellingen beheren (`selfservice.manage`) | ja | nee |
| Installatieaanvragen goedkeuren en hulpvragen afhandelen (`selfservice.handle`) | ja | ja |

## Grenzen

- Selfservice zit alleen in de Windows-tray-app; Linux-apparaten hebben geen
  tray-venster.
