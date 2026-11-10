# Integrations

Dutch version: [integrations.nl.md](integrations.nl.md).

Under **Administration > Integrations** you connect FleetWarden to your service
desk and to any other system with an HTTP API. Self-service tickets from the
tray, new alerts and new devices can use an integration. Only users with
`settings.manage` (administrators) see this page.

## Service desks

Ready-made connectors create a ticket with the device report, look up the
requester by the e-mail address of the logged-on user, add private notes and
read the ticket status back.

| Service desk | API | Authentication | Requester |
| --- | --- | --- | --- |
| TOPdesk | `/tas/api/incidents` | operator + application password | `callerLookup.email` |
| Freshservice | API v2 `/api/v2/tickets` | API key | `email` |
| Jira Service Management | `/rest/servicedeskapi/request` | e-mail + API token | `raiseOnBehalfOf` |
| ServiceNow | Table API `incident` | user name + password | `sys_user` by e-mail |
| Zendesk | `/api/v2/tickets.json` | e-mail + API token | `requester.email` |
| HaloITSM | `/api/Tickets` | OAuth 2.0 client credentials (scope `all`) | `user_emailaddress` |
| Zammad | `/api/v1/tickets` | access token | `customer_id: guess:<e-mail>` |
| GLPI | REST API v1 (`apirest.php`) | user token (+ app token) | requester notification e-mail |

Settings per connector:

- **Connection**: URL and credentials. Secrets are sealed with the server's
  secret key (like the SMTP and SSO secrets) and never sent back to the browser;
  leave a secret field empty to keep the stored value.
- **New tickets**: fallback requester (used when the user's e-mail address is
  unknown or not found), subject template (default
  `[{{device.hostname}}] {{ticket.subject}}`) and the desk's own fields such as
  group, category or request type.
- **Urgency to priority**: what the user picks in the tray (low, normal, high)
  and the value it becomes in the service desk. Empty sends no priority.
- **Two-way**: read the ticket status back for the tray, and log FleetWarden
  actions (chat, remote sessions) as private notes.

Use **Test connection** for the credentials and **Send test ticket** to create a
real ticket with the saved settings.

## Custom API integration

For a system without a ready-made connector, define the call yourself:

- **Method and URL**: GET, POST, PUT, PATCH or DELETE. Variables may be used in
  the path and query (they are URL-encoded).
- **Authentication**: none, bearer token, Basic, API key in a header, OAuth 2.0
  client credentials (token cached until it expires) or an HMAC signature:
  `X-FleetWarden-Timestamp` and `X-FleetWarden-Signature: sha256=<hex>` over
  `timestamp + "." + body`.
- **Headers** (at most 20; `Authorization`, `Content-Type` and `Host` are set by
  FleetWarden) and a **body template** in JSON, form, XML or text.
- **Response**: a JSON path such as `$.ticket.number` for the ticket number and
  link, shown to the user in the tray. Timeout 1 to 120 seconds.

### Variables

Write `{{name}}`. Values are escaped for the content type (inside a JSON string,
XML text, form value). Filters: `{{name | json}}` gives a quoted JSON string,
`| raw` no escaping, `| url` query escaping, `| html` HTML escaping. Unknown
variables become empty.

`event`, `time`, `device.id`, `device.hostname`, `device.url`, `device.ip`,
`device.os`, `device.serial`, `device.model`, `device.group`, `user.name`,
`user.email`, `user.displayName`, `ticket.subject`, `ticket.description`,
`ticket.urgency`, `ticket.category`, `report.text`, `report.html`,
`report.json`, `alert.title`, `alert.rule`, `alert.kind`, `alert.subject`.

`device.url` needs the public URL under Settings (or Reverse proxy); without it
the link is empty for calls that do not come from the web interface.

## Triggers

- **Self-service tickets**: chosen under Self-service > Settings.
- **New alerts** (`alert.opened`): one ticket or call per alert that opens,
  optionally only for some alert rules.
- **New devices** (`device.enrolled`, custom API only): once per new device
  (not when an agent is reinstalled on the same machine).

Calls run in the background, at most four at once. A network error, HTTP 429
or 5xx is retried after 10 seconds and after 1 minute. Every request is logged
per integration (method, URL without query string, status, duration, error,
ticket number) and kept 30 days. An integration that self-service still uses
cannot be deleted. Every change, test and test ticket is in the audit log.
