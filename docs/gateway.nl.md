# Secure gateway (DMZ)

Met de secure gateway bereiken agents buiten je netwerk (laptops thuis,
vestigingen zonder VPN) de managementserver, **zonder dat de managementserver
vanaf internet bereikbaar is**. Het is een kleine doorgeefserver op een aparte
machine in je DMZ.

```
agent ──HTTPS 443──▶ gateway (DMZ) ◀──tunnel 8444── managementserver (LAN)
```

- De **managementserver legt de tunnel** naar de gateway (van LAN naar DMZ, de
  richting die firewalls meestal toestaan). Een regel vanuit de DMZ naar je
  netwerk is niet nodig.
- De gateway geeft alleen agentverkeer door (`/agent/...`). De webinterface en
  de API zijn er nooit via bereikbaar.
- De gateway bewaart niets behalve zijn eigen sleutel, de sleutel van de
  gekoppelde server en zijn certificaten. Geen inventaris, geen wachtwoorden,
  geen tokens.
- Agents vertrouwen alleen de serversleutel die ze bij het inschrijven hebben
  vastgelegd en elk bericht is ondertekend, dus de gateway kan geen berichten
  wijzigen of toevoegen. Remote control blijft end-to-end versleuteld.

## Vereisten

- Een Debian 13-machine (amd64 of arm64; een kleine VM of container met 1 CPU
  en 512 MB RAM is ruim voldoende) in je DMZ.
- Een publieke DNS-naam voor agents, bijvoorbeeld `agents.example.com`.
- Firewallregels:
  - internet → gateway: TCP 443 (of de poort van de reverse proxy ervoor);
  - managementserver → gateway: TCP 8444 (de tunnel). Sta alleen de server toe.
  - Voor Let's Encrypt is ook TCP 80 vanaf internet nodig.

## 1. Installeer de gateway

Op de DMZ-machine:

```sh
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/main/installer/install-gateway.sh | sudo bash
# testkanaal:
curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/test/installer/install-gateway.sh | sudo bash -s -- --channel test
```

De installer vraagt hoe agents de gateway bereiken:

| Keuze | Wanneer | Wat er wordt ingesteld |
|---|---|---|
| Reverse proxy | Je hebt al Zoraxy, Nginx, Caddy, ... in de DMZ | Gewoon HTTP op `127.0.0.1:8080` (of een adres naar keuze); de proxy doet TLS. Zet het IP van de proxy als vertrouwde proxy. |
| Let's Encrypt | De gateway hangt rechtstreeks aan internet | Luistert op 443 en 80 en vraagt zelf een certificaat aan voor je domein. |
| Eigen certificaat | Je hebt een certificaat van je CA | Luistert op 443 met jouw certificaat en sleutel. |

Daarnaast vraagt hij het IP-adres van je managementserver: alleen dat adres mag
de tunnel openen (`FW_GW_TUNNEL_ALLOW`).

Aan het eind toont hij een **koppelcode** zoals `K7QF-2MXA-PZ4R-H9TD`. Je toont
hem altijd opnieuw met:

```sh
sudo fleetwarden-gateway pairing-code
```

Alle keuzes kunnen ook als optie, bijvoorbeeld
`--tls acme --domain agents.example.com --email it@example.com --tunnel-allow 10.0.10.5 --yes`.
De configuratie staat in `/etc/fleetwarden-gateway/gateway.env`.

Achter een reverse proxy: stuur WebSockets door en sta langdurige verbindingen
toe, net als voor de server zelf (zie [reverse-proxy.md](reverse-proxy.md)). Zet
in Zoraxy de WebSocket-timeout op 3600 seconden en zet *Refresh timeout on
activity* aan.

## 2. Koppel hem in de webinterface

*Admin > Secure gateway > Gateway toevoegen*:

- **Naam**: vrij te kiezen, bijvoorbeeld *DMZ Amsterdam*.
- **Tunneladres**: hoe de managementserver de gateway bereikt, zoals
  `10.0.20.5` of `gw.dmz.lan:8444` (poort 8444 tenzij je die hebt aangepast).
- **Publieke URL voor agents**: `https://agents.example.com`.
- **Koppelcode**: uit stap 1. Hij werkt één keer.

De server maakt verbinding, beide kanten bewijzen hun sleutel en leggen die van
de ander vast, en de gateway staat op *Verbonden*. **Testen** controleert de
tunnel en of de publieke URL echt via deze gateway bij deze server uitkomt. (Als
de server zijn eigen publieke adres niet kan bereiken, bijvoorbeeld zonder
hairpin-NAT, faalt die tweede controle van binnenuit; test dan vanaf een apparaat
buiten het netwerk.)

## 3. Agents

Elke agent krijgt de lijst met serveradressen, in deze volgorde:

1. de eigen externe basis-URL van de server (*Instellingen* of *Reverse proxy*);
2. elke ingeschakelde gateway.

Een agent probeert ze op volgorde en gaat door als er een niet antwoordt. Een
laptop gebruikt dus op kantoor het interne adres en thuis de gateway, zonder dat
je iets hoeft te doen. Bestaande agents krijgen de lijst vanzelf bij hun
volgende verbinding. Op de apparaatpagina staat *via gateway ...* als een
apparaat via een gateway verbonden is.

Om agents te installeren op apparaten die nu buiten het netwerk zijn, kies je de
gateway bij **Verbinden via** in de wizard *Client toevoegen*: de commando's en
downloads wijzen dan naar de gateway.

## Updates

De gatewaypagina toont de versie van de gateway. Als die afwijkt van de server,
laat **Gateway bijwerken** de gateway de versie van de server installeren vanaf
de officiële GitHub-release (checksums gecontroleerd). De tunnel valt een paar
seconden weg tijdens de herstart. Het installatiecommando opnieuw uitvoeren werkt
de gateway ook bij en houdt de configuratie en koppeling.

## Opnieuw koppelen

Na een herinstallatie van de gateway of om hem aan een andere server te hangen:

```sh
sudo fleetwarden-gateway reset-pairing
```

Dit vergeet de gekoppelde server, houdt de gatewaysleutel en toont een nieuwe
code. Gebruik daarna **Opnieuw koppelen** op de gatewaypagina met de nieuwe code.

## Beveiliging

- De tunnel gebruikt TLS met een zelfondertekend certificaat dat de gateway
  aanmaakt. De handshake ondertekent de hash van dat certificaat met beide
  sleutels, dus een man-in-the-middle met een ander certificaat komt er niet
  doorheen. Een eigen tunnelcertificaat kan met `FW_GW_TUNNEL_CERT` en
  `FW_GW_TUNNEL_KEY`.
- Daarom moet de tunnel de gateway rechtstreeks bereiken (geen proxy die TLS
  openbreekt ertussen).
- Na vijf verkeerde koppelcodes is koppelen 15 minuten geblokkeerd.
- De gateway draait als gebruiker zonder rechten met een geharde systemd-unit;
  alleen de updater draait als root en accepteert niets anders dan een
  versienummer.

## Configuratie (`/etc/fleetwarden-gateway/gateway.env`)

| Variabele | Standaard | Betekenis |
|---|---|---|
| `FW_GW_TLS` | `proxy` | `proxy`, `files` of `acme` |
| `FW_GW_LISTEN` | `127.0.0.1:8080` (proxy) / `:443` | Luisteradres voor agents |
| `FW_GW_TRUSTED_PROXIES` | `127.0.0.1/32,::1/128` | Proxies waarvan `X-Forwarded-For` vertrouwd wordt (proxymodus) |
| `FW_GW_TLS_CERT`, `FW_GW_TLS_KEY` | | Certificaat en sleutel (files-modus) |
| `FW_GW_ACME_DOMAINS`, `FW_GW_ACME_EMAIL` | | Let's Encrypt (acme-modus) |
| `FW_GW_TUNNEL_LISTEN` | `:8444` | Luisteradres voor de tunnel |
| `FW_GW_TUNNEL_ALLOW` | (iedereen) | Adressen die de tunnel mogen openen |
| `FW_GW_TUNNEL_CERT`, `FW_GW_TUNNEL_KEY` | gegenereerd | Eigen tunnelcertificaat |

Logboek: `journalctl -u fleetwarden-gateway`. Health check voor monitoring:
`GET /healthz` op het agentadres geeft `200` met `"server":"connected"`, of `503`
zolang de managementserver niet verbonden is.
