# Meldingen

*English: [alerts.md](alerts.md)*

**Meldingen** in het menu toont de apparaten die nu aandacht vragen. Het rode
getal ernaast is het aantal open meldingen dat nog niemand heeft bevestigd;
het dashboard toont het ook. Een melding opent als aan de voorwaarde van een
regel is voldaan en sluit vanzelf zodra het probleem weg is.

## Regels

Er zijn drie soorten regels:

- **Een apparaat is offline**: de agent heeft zich de gekozen tijd niet
  gemeld (minstens 5 minuten; agents melden zich elke minuut).
- **Een schijf is bijna vol**: een schijf heeft minder vrije ruimte dan het
  gekozen percentage. Schijven kleiner dan 2 GB (EFI- en herstelpartities)
  tellen niet mee. Een melding blijft open tot de schijf minstens één
  procentpunt meer vrije ruimte heeft dan de grens, zodat hij niet steeds
  opent en sluit.
- **De garantie afloopt**: de garantiedatum van het apparaat (uit
  [Assets](assets.nl.md)) ligt hooguit het gekozen aantal dagen verderop (0
  tot 365), of is al voorbij. De melding sluit als er een latere datum wordt
  ingevuld.

Elke regel kan beperkt worden tot apparaatgroepen en tot werkplekken, servers
of hypervisors, en mailt maximaal 20 adressen in het Engels of Nederlands,
desgewenst ook als de melding is opgelost. Zonder ontvangers zijn de
meldingen alleen in de webinterface zichtbaar. Nieuwe installaties beginnen
met regels zonder ontvangers: *Server offline* (servers en hypervisors,
15 minuten), *Low disk space* (10%) en, sinds assetbeheer, *Warranty ending* (60 dagen). De regels worden elke minuut
gecontroleerd; één e-mail bevat alle meldingen die een regel tegelijk opende.

Agents sturen hun vrije schijfruimte mee met elke heartbeat, dus een
schijfmelding opent binnen een paar minuten. Agents ouder dan deze versie
melden het alleen met hun inventaris. Na een herstart van de server wachten
offline-meldingen de gekozen tijd, zodat de agents eerst opnieuw kunnen
verbinden.

## Rechten

Iedereen die apparaten mag bekijken ziet de meldingen van zijn groepen.
**Bevestigen** (Beheerders en Technici, recht *Meldingen bevestigen en
meldingsregels beheren*) markeert een melding als gezien: hij blijft in de
lijst maar telt niet meer mee in het rode getal. De regels kunnen alleen
gewijzigd worden door gebruikers met dat recht die alle groepen zien, omdat
een regel naar de hele vloot kijkt.

## API

Zie de tabel in [alerts.md](alerts.md#api).
