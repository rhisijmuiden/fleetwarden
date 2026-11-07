# Running FleetWarden behind a reverse proxy

FleetWarden is designed to sit behind a reverse proxy that terminates TLS
(including Let's Encrypt). The server listens on `127.0.0.1:8080` by default.

Requirements for any proxy:

- Forward WebSocket upgrades (agents and remote control use WebSockets).
- Send `X-Forwarded-For` and `X-Forwarded-Proto`.
- Allow long-lived connections (agents stay connected; use a read timeout of at least 1 hour).

If the proxy runs on another host, set in `/etc/fleetwarden/server.env`:

```sh
FW_LISTEN=0.0.0.0:8080
FW_TRUSTED_PROXIES=192.0.2.10        # the proxy's IP address
```

Only addresses in `FW_TRUSTED_PROXIES` may set `X-Forwarded-For`, so audit logs
and rate limits see the real client IP and cannot be spoofed.

## Zoraxy

1. *HTTP Proxy > Create proxy rule*: matching domain `em.example.com`, target
   `127.0.0.1:8080` (or the server IP), without TLS to the backend.
2. Enable *Require TLS* and request a Let's Encrypt certificate under *TLS / SSL certificates*.
3. WebSocket support is on by default; leave *Disable websocket proxy* unchecked.

## Nginx

```nginx
map $http_upgrade $connection_upgrade { default upgrade; '' close; }

server {
    listen 443 ssl http2;
    server_name em.example.com;
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
        client_max_body_size 2g;   # software packages upload
    }
}
```

## Caddy

```caddyfile
em.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy handles certificates, WebSockets and forwarded headers automatically.
