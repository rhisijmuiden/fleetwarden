# AI-assistenten (MCP-server)

Engelse versie: [mcp.md](mcp.md).

FleetWarden heeft een ingebouwde [Model Context Protocol](https://modelcontextprotocol.io)-server,
zodat AI-assistenten zoals Claude je apparaten kunnen opzoeken en beheren.
Hij staat **standaard uit**. Zet hem aan onder **Beheer > AI-assistenten (MCP)**.

- Adres: `https://<jouw FleetWarden>/mcp` (streamable HTTP, stateless). Gebruikt
  de publieke URL uit Instellingen en werkt dus via je reverse proxy, zonder
  extra regel.
- Een assistent handelt altijd als **één FleetWarden-gebruiker**: dezelfde rol,
  dezelfde apparaatgroepen. Tools waarvoor die gebruiker geen recht heeft,
  worden niet aangeboden.
- **Elke tool-aanroep komt in het auditlogboek** (actie `mcp.tool`) onder die
  gebruiker, met de naam van het token en de argumenten. Wijzigingen via een
  tool krijgen daarnaast hun gewone auditregel.

## Toegangsniveaus

De beheerder stelt in wat een assistent maximaal mag; elk token of elke
koppeling kiest een niveau tot dat maximum.

| Niveau | Tools |
|---|---|
| Alleen lezen (standaard) | overzicht, apparaten, software, updates, kwetsbaarheden, beveiliging, meldingen, assets, licenties, rapporten, energiegeschiedenis, processen, services, logboeken, auditlogboek |
| Lezen en uitvoeren | ook: scan aanvragen, update goedkeuren of afwijzen, updates nu installeren, software installeren, melding afhandelen, apparaat wekken |
| Volledig (ingrijpend) | ook: apparaat herstarten of afsluiten, service starten/stoppen/herstarten, proces beëindigen. Deze vragen `confirm` met de exacte hostnaam, en de server zegt de assistent het jou eerst te vragen. |

Losse tools zet je uit op de instellingenpagina.

## Koppelen

**Claude (claude.ai, Desktop, mobiel)** - met OAuth aan (standaard):
Instellingen > Connectors > Aangepaste connector toevoegen, plak de URL. Je
meldt je aan bij FleetWarden (SSO en TOTP gelden), kiest het niveau en geeft
toestemming.

**Claude Code** - `claude mcp add --transport http fleetwarden https://<host>/mcp`
(meldt zich de eerste keer aan via OAuth), of voeg
`--header "Authorization: Bearer <token>"` toe met een persoonlijk token.

**Andere clients (Cursor, VS Code, ...)** - maak een persoonlijk token op de
instellingenpagina of onder Profiel; het venster toont de JSON-configuratie.

## Tokens en OAuth

- Persoonlijke tokens (`fwm_...`) worden één keer getoond, als SHA-256-hash
  opgeslagen en verlopen na 7-365 dagen of nooit. Elke gebruiker maakt eigen
  tokens onder Profiel; beheerders zien en intrekken alles.
- OAuth 2.1 met PKCE (S256) en dynamische clientregistratie, volgens de
  MCP-autorisatiespecificatie. Toegangstokens zijn een uur geldig; refresh
  tokens wisselen bij elk gebruik en een koppeling vervalt na 30 dagen zonder
  gebruik.
- De MCP-server uitzetten stopt alle tokens en koppelingen tegelijk. Een
  gebruiker uitschakelen, of een verplichte wachtwoordwijziging, stopt die van
  hem.
- Mislukte tokenpogingen worden per adres beperkt (30 per 15 minuten).
