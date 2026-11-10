# Koppelingen

Engelse versie: [integrations.md](integrations.md).

Onder **Administratie > Koppelingen** koppel je FleetWarden aan je servicedesk
en aan elk ander systeem met een HTTP-API. Selfservicemeldingen uit de tray,
nieuwe waarschuwingen en nieuwe apparaten kunnen een koppeling gebruiken. Alleen
gebruikers met `settings.manage` (beheerders) zien deze pagina.

## Servicedesks

Kant-en-klare koppelingen maken een ticket aan met het apparaatrapport, zoeken
de aanmelder op het e-mailadres van de aangemelde gebruiker, voegen interne
notities toe en lezen de status van het ticket terug.

| Servicedesk | API | Authenticatie | Aanmelder |
| --- | --- | --- | --- |
| TOPdesk | `/tas/api/incidents` | operator + applicatiewachtwoord | `callerLookup.email` |
| Freshservice | API v2 `/api/v2/tickets` | API-sleutel | `email` |
| Jira Service Management | `/rest/servicedeskapi/request` | e-mail + API-token | `raiseOnBehalfOf` |
| ServiceNow | Table API `incident` | gebruikersnaam + wachtwoord | `sys_user` op e-mail |
| Zendesk | `/api/v2/tickets.json` | e-mail + API-token | `requester.email` |
| HaloITSM | `/api/Tickets` | OAuth 2.0 client credentials (scope `all`) | `user_emailaddress` |
| Zammad | `/api/v1/tickets` | toegangstoken | `customer_id: guess:<e-mail>` |
| GLPI | REST API v1 (`apirest.php`) | gebruikerstoken (+ app-token) | e-mail voor meldingen aan de aanvrager |

Instellingen per koppeling:

- **Verbinding**: URL en inloggegevens. Geheimen worden versleuteld met de
  geheime sleutel van de server (zoals de SMTP- en SSO-geheimen) en gaan nooit
  terug naar de browser; laat een geheim veld leeg om de opgeslagen waarde te
  houden.
- **Nieuwe tickets**: reserve-aanmelder (als het e-mailadres van de gebruiker
  onbekend is of niet gevonden wordt), sjabloon voor het onderwerp (standaard
  `[{{device.hostname}}] {{ticket.subject}}`) en de eigen velden van de
  servicedesk zoals groep, categorie of aanvraagtype.
- **Urgentie naar prioriteit**: wat de gebruiker in de tray kiest (laag,
  normaal, hoog) en de waarde die dat in de servicedesk wordt. Leeg stuurt geen
  prioriteit mee.
- **Terugkoppeling**: de status van het ticket teruglezen voor de tray, en
  FleetWarden-acties (chat, overnames) als interne notitie vastleggen.

Met **Verbinding testen** controleer je de inloggegevens, met **Testticket
sturen** maak je een echt ticket aan met de opgeslagen instellingen.

## Eigen API-koppeling

Voor een systeem zonder kant-en-klare koppeling bepaal je de aanroep zelf:

- **Methode en URL**: GET, POST, PUT, PATCH of DELETE. Variabelen mogen in het
  pad en de query (ze worden URL-gecodeerd).
- **Authenticatie**: geen, bearer-token, Basic, API-sleutel in een header,
  OAuth 2.0 client credentials (token bewaard tot hij verloopt) of een
  HMAC-handtekening: `X-FleetWarden-Timestamp` en
  `X-FleetWarden-Signature: sha256=<hex>` over `tijdstip + "." + body`.
- **Headers** (maximaal 20; `Authorization`, `Content-Type` en `Host` zet
  FleetWarden zelf) en een **body-sjabloon** in JSON, formulier, XML of tekst.
- **Antwoord**: een JSON-pad zoals `$.ticket.number` voor het ticketnummer en de
  link, zichtbaar voor de gebruiker in de tray. Time-out 1 tot 120 seconden.

### Variabelen

Schrijf `{{naam}}`. Waarden worden ge-escaped voor de soort inhoud (binnen een
JSON-string, XML-tekst, formulierwaarde). Filters: `{{naam | json}}` geeft een
JSON-string met aanhalingstekens, `| raw` geen escaping, `| url` voor een query,
`| html` voor HTML. Onbekende variabelen worden leeg.

`event`, `time`, `device.id`, `device.hostname`, `device.url`, `device.ip`,
`device.os`, `device.serial`, `device.model`, `device.group`, `user.name`,
`user.email`, `user.displayName`, `ticket.subject`, `ticket.description`,
`ticket.urgency`, `ticket.category`, `report.text`, `report.html`,
`report.json`, `alert.title`, `alert.rule`, `alert.kind`, `alert.subject`.

`device.url` heeft de publieke URL onder Instellingen (of Reverse proxy) nodig;
zonder die URL is de link leeg bij aanroepen die niet uit de webinterface komen.

## Triggers

- **Selfservicemeldingen**: te kiezen onder Selfservice > Instellingen.
- **Nieuwe waarschuwingen** (`alert.opened`): één ticket of aanroep per
  waarschuwing die opent, eventueel alleen voor sommige regels.
- **Nieuwe apparaten** (`device.enrolled`, alleen eigen API): eenmaal per nieuw
  apparaat (niet als de agent op dezelfde machine opnieuw wordt geïnstalleerd).

Aanroepen lopen op de achtergrond, maximaal vier tegelijk. Bij een
netwerkfout, HTTP 429 of 5xx volgt een nieuwe poging na 10 seconden en na 1
minuut. Elk verzoek wordt per koppeling vastgelegd (methode, URL zonder query,
status, duur, fout, ticketnummer) en 30 dagen bewaard. Een koppeling die
selfservice nog gebruikt kan niet worden verwijderd. Elke wijziging, test en
testticket staat in het auditlog.
