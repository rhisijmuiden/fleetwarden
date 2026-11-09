# FleetWarden achter een reverse proxy

FleetWarden is gemaakt om achter een reverse proxy te staan die TLS afhandelt
(ook Let's Encrypt). De server luistert standaard op `127.0.0.1:8080`. Engelse
versie: [reverse-proxy.md](reverse-proxy.md).

**Het makkelijkst:** open *Administratie > Reverse proxy* in de webinterface.
Daar zie je hoe verzoeken de server bereiken, stel je de externe basis-URL en
vertrouwde proxy's in (zonder herstart), maak je een configuratie voor nginx,
Caddy, HAProxy of Zoraxy met het adres van deze server al ingevuld, en
controleert de knop **Test uitvoeren** DNS, het certificaat, de doorgestuurde
headers, het agent-endpoint, WebSockets en een upload van 16 MB van begin tot
eind.

Eisen voor elke proxy:

- Stuur WebSocket-upgrades door (agents en remote control gebruiken
  WebSockets).
- Stuur `X-Forwarded-For` en `X-Forwarded-Proto` mee.
- Sta langlopende verbindingen toe (agents blijven verbonden; gebruik een
  read-timeout van minstens 1 uur).

Draait de proxy op een andere machine, zet dan in `/etc/fleetwarden/server.env`:

```sh
FW_LISTEN=0.0.0.0:8080
FW_TRUSTED_PROXIES=192.0.2.10        # het IP-adres van de proxy
```

Alleen adressen in `FW_TRUSTED_PROXIES` of in de vertrouwde proxy's van de
webinterface mogen `X-Forwarded-For`, `-Proto` en `-Host` zetten, zodat
auditlog en inloglimieten het echte IP-adres van de client zien en dat niet te
vervalsen is. Een gewijzigde `FW_LISTEN` vraagt een herstart:
`sudo systemctl restart fleetwarden-server`.

## Zoraxy

1. *HTTP Proxy > Create proxy rule*: matching domain `em.voorbeeld.nl`, target
   `127.0.0.1:8080` (of het IP van de server), zonder TLS naar de backend.
2. Zet *Require TLS* aan en vraag een Let's Encrypt-certificaat aan onder
   *TLS / SSL certificates*.
3. WebSocket-ondersteuning staat standaard aan; laat *Disable WebSocket* uit.
   Zet *WebSocket Timeout* op `3600` en zet *Refresh timeout on activity* aan
   (anders is het standaard maximaal 300 seconden per verbinding).
4. Laat *Block Common Exploits* uit voor deze regel; dat kan agentverkeer en
   uploads van installers blokkeren.

## Nginx

```nginx
map $http_upgrade $connection_upgrade { default upgrade; '' close; }

server {
    listen 443 ssl http2;
    server_name em.voorbeeld.nl;
    # ssl_certificate ... (certbot)

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_read_timeout 3600s;
        client_max_body_size 2g;   # uploads van softwarepakketten
    }
}
```

## Caddy

```caddyfile
em.voorbeeld.nl {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy regelt certificaten, WebSockets en doorgestuurde headers zelf.
