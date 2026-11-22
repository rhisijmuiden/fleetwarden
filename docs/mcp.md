# AI assistants (MCP server)

Dutch version: [mcp.nl.md](mcp.nl.md).

FleetWarden has a built-in [Model Context Protocol](https://modelcontextprotocol.io)
server, so AI assistants such as Claude can look up and manage your devices.
It is **off by default**. Turn it on under **Administration > AI assistants (MCP)**.

- Endpoint: `https://<your FleetWarden>/mcp` (streamable HTTP, stateless). It
  uses the public URL from Settings, so it works through your reverse proxy;
  no extra proxy rule is needed.
- An assistant always acts as **one FleetWarden user**: same role, same device
  groups. Tools the user has no permission for are not offered.
- **Every tool call is written to the audit log** (action `mcp.tool`) under that
  user, with the token's name and the arguments. Changes made through a tool
  also get their normal audit entry.

## Access levels

The administrator sets the most an assistant may do; each token or connection
picks a level up to that maximum.

| Level | Tools |
|---|---|
| Read only (default) | overview, devices, software, updates, vulnerabilities, security, alerts, assets, licences, reports, power history, processes, services, event logs, audit log |
| Read and act | also: request a scan, approve or decline an update, install updates now, install software, acknowledge an alert, wake a device |
| Full (destructive) | also: restart or shut down a device, start/stop/restart a service, end a process. These need `confirm` set to the exact host name, and the server tells the assistant to ask you first. |

Single tools can be turned off on the settings page.

## Connecting

**Claude (claude.ai, Desktop, mobile)** - with OAuth on (default): Settings >
Connectors > Add custom connector, paste the URL. You sign in to FleetWarden
(SSO and TOTP apply), choose the access level and approve.

**Claude Code** - `claude mcp add --transport http fleetwarden https://<host>/mcp`
(signs in with OAuth on first use), or add
`--header "Authorization: Bearer <token>"` with a personal token.

**Other clients (Cursor, VS Code, ...)** - make a personal token on the settings
page or under Profile; the dialog shows the JSON configuration:

```json
{ "mcpServers": { "fleetwarden": { "type": "http", "url": "https://<host>/mcp",
  "headers": { "Authorization": "Bearer fwm_..." } } } }
```

## Tokens and OAuth

- Personal tokens (`fwm_...`) are shown once, stored as SHA-256 hashes, expire
  after 7-365 days or never. Every user can make their own under Profile;
  administrators see and revoke all.
- OAuth 2.1 with PKCE (S256) and dynamic client registration, as the MCP
  authorization spec describes (`/.well-known/oauth-protected-resource`,
  `/.well-known/oauth-authorization-server`, `/oauth/register`,
  `/oauth/authorize`, `/oauth/token`, `/oauth/revoke`). Access tokens last one
  hour; refresh tokens rotate on every use and a connection ends after 30 days
  without use.
- Turning the MCP server off stops every token and connection at once.
  Disabling a user, or a forced password change, stops theirs.
- Failed token attempts are rate limited per address (30 per 15 minutes).
