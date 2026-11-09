# Beveiliging en hardening

Deze pagina beschrijft hoe FleetWarden zichzelf beschermt, wat de
security-review vóór versie 1.0 heeft gevonden en wat je als beheerder zelf kunt
doen om een installatie verder te beveiligen. Engelse versie:
[hardening.md](hardening.md).

## Security-review voor 1.0

De review (november 2026) omvatte de beheerserver, het agentprotocol, de
secure gateway, de installer en de packaging. Daarbij is:

- de code gelezen van elk endpoint zonder login (inloggen, SSO, inschrijven
  van agents, de agent-WebSocket, ondertekende agentdownloads en
  bestandsoverdrachten, remote-control-relays, proxy-probes, huisstijl);
- gecontroleerd dat elke API-route met een id de rechten én de
  apparaatgroep-scope van de ingelogde gebruiker afdwingt;
- `govulncheck` gedraaid voor de Go-standaardbibliotheek en modules, en
  `npm audit` voor de webinterface;
- gekeken naar de systemd-units, bestandsrechten en HTTP-headers.

### Bevindingen en oplossingen

| # | Bevinding | Ernst | Status |
|---|-----------|-------|--------|
| 1 | Gebouwd met Go 1.26.0: `govulncheck` meldde 41 bereikbare kwetsbaarheden in de standaardbibliotheek (onder meer net/http, crypto/x509, net/url, os) en twee in `golang.org/x/net`. | Hoog | Opgelost: Go 1.26.10 en `x/net` 0.62.0. CI draait nu bij elke wijziging `govulncheck` en `npm audit`. |
| 2 | Met een enrollment-token van een technicus die beperkt is tot enkele apparaatgroepen kon een apparaat met `GROUP=` in elke groep terechtkomen, en zo de configuraties en software van die groep krijgen. | Middel | Opgelost: de groep kiezen werkt alleen binnen de scope van de maker van het token; een geweigerde keuze komt in het auditlog (`device.enroll.group_denied`). |
| 3 | Elke inlogpoging draait argon2id met 64 MiB geheugen; veel gelijktijdige pogingen konden het geheugen van de server uitputten. | Middel | Opgelost: hooguit vier wachtwoordberekeningen tegelijk; andere pogingen wachten. |
| 4 | Tweestapsverificatie uitzetten vroeg om het wachtwoord zonder limiet op foute pogingen. | Laag | Opgelost: foute wachtwoorden tellen mee voor de inloglimiet per gebruiker. |
| 5 | Een stuk van een bestandsoverdracht zonder `Content-Length` telde niet mee voor de maximale grootte. | Laag | Opgelost: de controle gebruikt het werkelijk ontvangen aantal bytes. |
| 6 | De server-unit miste enkele systemd-sandboxopties en een bestandslimiet voor duizenden agentverbindingen. | Laag | Opgelost: zie de unit hieronder. |
| 7 | De headers `Permissions-Policy`, `Cross-Origin-Opener-Policy` en `Cross-Origin-Resource-Policy` ontbraken. | Info | Opgelost. |

### Geaccepteerde risico's

- **Ondertekende HTTP-verzoeken van agents zijn vijf minuten opnieuw af te
  spelen.** De handtekening dekt methode, pad, apparaat-id en tijd, niet de
  inhoud. De verzoeken gaan over TLS, horen bij één apparaat en bij een open
  taak, dus opnieuw afspelen vraagt eerst een gebroken TLS-verbinding.
- **Enrollment-tokens zijn geheimen.** Wie een geldig token heeft, kan een
  apparaat in de groep ervan inschrijven. Geef tokens een vervaldatum en een
  maximum aantal keer gebruik, en trek tokens in die je niet meer nodig hebt.
- **De agent draait als root / SYSTEM.** Dat moet om updates en software te
  installeren. Alles wat hij uitvoert komt in berichten die zijn ondertekend
  met de serversleutel die de agent bij het inschrijven heeft vastgelegd.
- **De ingebouwde limieten gelden per serverproces.** Na een herstart beginnen
  ze opnieuw.

## Ingebouwde bescherming

**Accounts en sessies**

- Wachtwoorden: argon2id (64 MiB, 3 rondes), minstens 12 tekens, zonder de
  gebruikersnaam. Een onbekende gebruiker kost evenveel tijd als een fout
  wachtwoord.
- Inloglimieten: 20 mislukte pogingen per IP-adres en 8 per gebruikersnaam in
  15 minuten.
- Optionele tweestapsverificatie met TOTP; codes zijn niet te hergebruiken.
- Sessies: willekeurige tokens van 256 bits, alleen als hash opgeslagen;
  cookies met `HttpOnly`, `SameSite=Lax` en `Secure`; 2 uur inactief en 12 uur
  maximaal. Je wachtwoord wijzigen meldt je andere sessies af.
- De nood-beheerder is te beperken tot bepaalde netwerken.
- Rollen (beheerder, technicus, alleen-lezen) met scopes op apparaatgroepen;
  elke apparaat-API controleert beide.

**Webinterface**

- Strikte Content Security Policy (geen inline scripts),
  `X-Frame-Options: DENY`, `nosniff`, een referrer-policy, `Permissions-Policy`
  en cross-origin-isolatie. HSTS zet je aan onder *Reverse proxy*.
- Elke API-aanroep die iets wijzigt vraagt een eigen header die een browser
  niet cross-site kan meesturen, dus cross-site request forgery werkt niet.
- Geüploade SVG-logo's worden met een sandbox-policy geserveerd.

**Agents**

- Elke agent heeft een eigen Ed25519-sleutelpaar. Bij het inschrijven bewijst
  de agent dat hij de privésleutel heeft; de agent legt de publieke sleutel van
  de server vast.
- Elk WebSocket-bericht is in beide richtingen ondertekend en genummerd, dus
  het is niet te vervalsen, opnieuw af te spelen of terug te kaatsen.
- Downloads door agents (updates, software, drivers, bestandsoverdrachten) zijn
  ondertekende verzoeken, en alleen een apparaat met een open taak voor het
  bestand krijgt het. Pakketten worden vóór installatie gecontroleerd op hun
  SHA-256-hash.
- Mislukte agent-authenticaties zijn per IP-adres begrensd.

**Geheimen**

- Geheimen in de database (SMTP- en backupwachtwoorden, TOTP-geheimen,
  BitLocker-sleutels, licentiesleutels) zijn versleuteld met een sleutel in
  `/var/lib/fleetwarden`.
- Backups worden met jouw wachtwoordzin versleuteld voordat ze de server
  verlaten.

## Checklist voor beheerders

1. **Laat de server alleen via je reverse proxy bereikbaar zijn.** Houd de
   standaard `FW_LISTEN=127.0.0.1:8080` aan, of zet een firewall op poort 8080
   zodat alleen de proxy erbij kan. Zet in `FW_TRUSTED_PROXIES` alleen het adres
   van de proxy.
2. **Gebruik overal HTTPS** en zet HSTS aan onder *Beheer > Reverse proxy* zodra
   het certificaat werkt.
3. **Stel de publieke URL in** onder *Beheer > Reverse proxy*, zodat links en
   agent-installers nooit van de `Host`-header afhangen.
4. **Zet TOTP aan** voor elk lokaal account, of gebruik inloggen met Entra ID
   met meervoudige verificatie. Beperk de nood-beheerder tot je
   beheernetwerk.
5. **Geef technici een groepsscope** in plaats van toegang tot alles.
6. **Gebruik kortlevende enrollment-tokens** met een maximum aantal keer
   gebruik.
7. **Gebruik de secure gateway** voor agents op internet, zodat de beheerserver
   zelf niet open staat.
8. **Stel backups naar een externe locatie in** met een sterke wachtwoordzin en
   test een restore.
9. **Houd FleetWarden actueel** via *Over > Controleer op updates*; het
   productiekanaal krijgt alleen geteste releases.
10. **Bekijk het auditlog** regelmatig (*Auditlog* in het menu): daarin staat
    elke aanmelding, mislukte aanmelding en wijziging.

## De systemd-unit van de server

`fleetwarden-server.service` draait als de gewone gebruiker `fleetwarden`
zonder capabilities, met een alleen-lezen systeem (`ProtectSystem=strict`,
schrijven alleen in `/var/lib/fleetwarden` en `/var/log/fleetwarden`), een
eigen `/tmp` en apparaten, zonder toegang tot thuismappen, kernelinstellingen,
klok of hostnaam, zonder nieuwe rechten, alleen IP-, Unix- en netlink-sockets
en een umask van `0027`. `LimitNOFILE=65536` geeft ruimte voor duizenden
agentverbindingen.

## Een kwetsbaarheid melden

Meld beveiligingsproblemen vertrouwelijk via de knop *Report a vulnerability*
op het tabblad *Security* van de repository, niet in een openbare issue.
