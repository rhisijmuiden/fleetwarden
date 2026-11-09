// Generates reverse proxy configuration examples for this server. The output
// is configuration code, so it is the same in every UI language.

export type ProxyKind = "nginx" | "caddy" | "haproxy" | "zoraxy";
export type TLSMode = "acme" | "own";

export type ProxyInput = {
  domain: string; // em.example.com
  upstream: string; // host:port the proxy forwards to
  tlsMode: TLSMode;
  maxUploadMiB: number;
};

// Uploads (software packages, agent installers) plus room for headers.
const bodyLimitMiB = (i: ProxyInput) => i.maxUploadMiB + 100;

const ownCert = "/etc/ssl/fleetwarden/fullchain.pem";
const ownKey = "/etc/ssl/fleetwarden/privkey.pem";

export function nginxConfig(i: ProxyInput): string {
  const cert = i.tlsMode === "acme" ? `/etc/letsencrypt/live/${i.domain}/fullchain.pem` : ownCert;
  const key = i.tlsMode === "acme" ? `/etc/letsencrypt/live/${i.domain}/privkey.pem` : ownKey;
  const acme =
    i.tlsMode === "acme"
      ? `# Certificate (once): apt install certbot python3-certbot-nginx && certbot certonly --nginx -d ${i.domain}
# certbot renews it automatically and reloads nginx.
`
      : `# Certificate: place the full chain in ${ownCert} and the key in ${ownKey}.
`;
  return `# /etc/nginx/sites-available/fleetwarden
# Enable:  ln -s /etc/nginx/sites-available/fleetwarden /etc/nginx/sites-enabled/
# Apply:   nginx -t && systemctl reload nginx
${acme}
# WebSockets (agents, remote control, live updates) need Upgrade/Connection.
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    listen [::]:80;
    server_name ${i.domain};

    location /.well-known/acme-challenge/ { root /var/www/html; }
    location / { return 301 https://$host$request_uri; }
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;  # nginx before 1.25.1: remove this line and use "listen 443 ssl http2;"
    server_name ${i.domain};

    ssl_certificate     ${cert};
    ssl_certificate_key ${key};
    ssl_protocols       TLSv1.2 TLSv1.3;

    # Software packages and agent installers up to ${i.maxUploadMiB / 1024} GiB, streamed to the server.
    client_max_body_size    ${bodyLimitMiB(i)}m;
    proxy_request_buffering off;

    location / {
        proxy_pass         http://${i.upstream};
        proxy_http_version 1.1;

        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host  $host;

        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        # Agents stay connected and remote sessions can take hours.
        proxy_read_timeout  3600s;
        proxy_send_timeout  3600s;
        # Stream report exports and downloads straight through.
        proxy_buffering off;
    }
}
`;
}

export function caddyConfig(i: ProxyInput): string {
  const tls =
    i.tlsMode === "acme"
      ? `    # Caddy requests and renews the Let's Encrypt certificate itself.
    # Ports 80 and 443 must be reachable from the internet.
`
      : `    tls ${ownCert} ${ownKey}
`;
  return `# /etc/caddy/Caddyfile
# Apply: caddy validate --config /etc/caddy/Caddyfile && systemctl reload caddy
# Caddy proxies WebSockets and sends X-Forwarded-For/-Proto/-Host by itself,
# and redirects http:// to https:// automatically.

${i.domain} {
${tls}
    # Software packages and agent installers up to ${i.maxUploadMiB / 1024} GiB.
    request_body {
        max_size ${bodyLimitMiB(i)}MB
    }

    reverse_proxy ${i.upstream} {
        # Stream responses (exports, remote control) without buffering.
        flush_interval -1
        # Caddy 2.7 or newer: keep agent and remote control connections open
        # for 5 minutes after a config reload, so not all agents reconnect at once.
        # stream_close_delay 5m
    }
}
`;
}

export function haproxyConfig(i: ProxyInput): string {
  const pem = `/etc/haproxy/certs/${i.domain}.pem`;
  const certHelp =
    i.tlsMode === "acme"
      ? `# Certificate (once), with certbot answering the challenge behind HAProxy:
#   certbot certonly --standalone --http-01-port 8888 -d ${i.domain} \\
#     --deploy-hook 'cat $RENEWED_LINEAGE/fullchain.pem $RENEWED_LINEAGE/privkey.pem > ${pem} && systemctl reload haproxy'
#   (first time: run the same cat command by hand, before starting HAProxy)
`
      : `# Certificate: one PEM file with the full chain followed by the private key:
#   cat fullchain.pem privkey.pem > ${pem} && chmod 600 ${pem}
`;
  const acmeAcl =
    i.tlsMode === "acme"
      ? `    acl acme path_beg /.well-known/acme-challenge/
    use_backend letsencrypt if acme
    http-request redirect scheme https code 301 if !{ ssl_fc } !acme
`
      : `    http-request redirect scheme https code 301 if !{ ssl_fc }
`;
  const acmeBackend =
    i.tlsMode === "acme"
      ? `
backend letsencrypt
    server certbot 127.0.0.1:8888
`
      : "";
  return `# /etc/haproxy/haproxy.cfg (add these sections)
# Apply: haproxy -c -f /etc/haproxy/haproxy.cfg && systemctl reload haproxy
${certHelp}
frontend fleetwarden
    mode http
    bind :80
    bind :443 ssl crt ${pem} alpn h2,http/1.1
${acmeAcl}
    # Real client address, scheme and host for FleetWarden.
    option forwardfor
    http-request set-header X-Forwarded-Proto https if { ssl_fc }
    http-request set-header X-Forwarded-Proto http if !{ ssl_fc }
    http-request set-header X-Forwarded-Host %[req.hdr(host)]

    # Headers must arrive quickly; bodies (uploads up to ${i.maxUploadMiB / 1024} GiB) may take long.
    timeout http-request 30s
    # Agents stay connected and remote sessions can take hours.
    timeout client 1h
    default_backend fleetwarden_servers

backend fleetwarden_servers
    mode http
    option httpchk GET /healthz
    http-check expect status 200
    timeout connect 10s
    timeout server 1h
    # WebSockets (agents, remote control, live updates) after the upgrade.
    timeout tunnel 1h
    server fleetwarden ${i.upstream} check
${acmeBackend}`;
}

export function exampleConfig(kind: ProxyKind, i: ProxyInput): string {
  switch (kind) {
    case "nginx":
      return nginxConfig(i);
    case "caddy":
      return caddyConfig(i);
    case "haproxy":
      return haproxyConfig(i);
    default:
      return "";
  }
}

export const configFileName: Record<ProxyKind, string> = {
  nginx: "fleetwarden.nginx.conf",
  caddy: "Caddyfile",
  haproxy: "fleetwarden.haproxy.cfg",
  zoraxy: "",
};
