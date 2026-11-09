# Load test

FleetWarden is built for 100 to 1,000 endpoints. Before version 1.0 the server
was tested with **2,000 simulated agents**, twice the upper end, to find the
limits and fix them. Dutch version: [load-test.nl.md](load-test.nl.md).

## The tool

`fleetwarden-loadtest` (in `cmd/fleetwarden-loadtest`) simulates agents with
the real protocol: enrollment with an Ed25519 key, the signed WebSocket
session, a full inventory (150 programs) when the server asks for one, and a
heartbeat with disk volumes every minute. At the same time it signs in to the
web interface and times the pages an administrator opens most. At the end it
drops all sessions at once, like a server restart, and measures how fast every
agent is back.

```sh
make loadtest
# Only on a test server: the simulated devices appear in the device list.
sudo fleetwarden-server enrollment-token --group Default
dist/linux-amd64/fleetwarden-loadtest -server https://em.example.com -token fwe_... \
  -agents 2000 -ramp 2m -duration 10m -api-user admin -api-password '...' -report result.json
```

The keys of the simulated agents are kept in `loadtest-agents.json`, so a
second run reconnects the same devices. Remove the simulated `LOADTEST-*`
devices and the state file afterwards with
`fleetwarden-loadtest -cleanup -server ... -api-user admin -api-password '...'`.

## Set-up

- One machine with **4 CPU cores and 15 GB RAM** running the server,
  PostgreSQL 16 *and* the load generator, so the numbers are pessimistic.
- 2,000 Windows agents connecting over 2 minutes, then 5 minutes of steady
  state, then a reconnect storm.
- The vulnerability sources were synchronised, and every simulated device
  runs the same unpatched Windows build: **2,678 open CVEs on each of the
  2,000 devices (5.4 million device matches)**. That is a worst case; a
  patched fleet has far fewer.

## Results

| | Before fixes | After fixes |
|---|---|---|
| Agents connected | 2,000 of 2,000 | 2,000 of 2,000 |
| Failed or dropped sessions | 2 of 200 in a first small run (see below) | 0 |
| Server memory (RSS) with 2,000 agents | 3.1 GB | 0.2 GB |
| All agents back after a reconnect storm | 3.8 s | 2.5 s |
| Heartbeat send time p95 | 1.0 ms | 0.4 ms |
| Vulnerability summary p50 / max | 2.9 s / 5.8 s | 1 ms (cached) / 0.7 s |
| Vulnerability list (500 CVEs, idle server) | 10.7 s | 1.6 s |
| Vulnerability list (100 CVEs, during the test) | 10.8 s | 3.1 s |
| Device list, dashboards, security, patches p95 | under 50 ms | under 25 ms |
| Installed software (all programs) p95 | 0.46 s | 0.49 s |

The database grew to 1.7 GB, mostly vulnerability matches and inventories.

## What was fixed

1. **Agents could drop their connection** when the server sent two messages
   at the same moment (on every connect several features send their state in
   parallel). The messages could be queued in a different order than they
   were numbered; the agent rejects a lower sequence number and reconnects.
   Signing and queueing are now one step per connection.
2. **3 GB of memory for WebSocket compression.** Every agent connection kept
   its own compressor (about 1.3 MB). The server now negotiates compression
   without context takeover, which shares compressors; existing agents
   accept that without an update.
3. **Vulnerability summary and list** counted distinct devices over millions
   of matches. They now reduce the matches per CVE first, count devices with
   one index lookup per device, and compute the exact numbers only for the
   CVEs on the page. The summary is cached for 30 seconds and refreshed as
   soon as matching or an accepted risk changes it.
4. **Database settings**: the connection pool is 20 connections (it was one
   per CPU core) and PostgreSQL's JIT compiler is off for FleetWarden's
   connections, which saved about 0.7 s per dashboard query. Add
   `pool_max_conns=N` or `jit=on` to `FW_DATABASE_URL` to override.
5. **Indexes**: an index on the device id as text (used by every agent
   message) and room on each table page so heartbeat updates stay cheap.
6. The systemd unit allows 65,536 open files.

## Sizing

| Endpoints | Server | Notes |
|---|---|---|
| up to 250 | 2 vCPU, 4 GB RAM, 40 GB disk | |
| 250 to 1,000 | 4 vCPU, 8 GB RAM, 80 GB disk | the recommended size |
| 1,000 to 2,000 | 4 to 8 vCPU, 8 to 16 GB RAM, 150 GB disk | tested, see above |

Disk space mostly goes to the software library and driver repository you
upload, and to the backups kept on the server. Give PostgreSQL a quarter of
the memory as `shared_buffers` on larger installations.

## Known limits

- **Searching the vulnerability list** in a fleet with millions of open
  matches takes a few seconds when the search term matches most of them (for
  example "windows"). Filtering by severity, KEV, group or device is fast.
- **Installed software** lists every program of every device in one view;
  with 2,000 devices and 150 programs each it takes about half a second.
- The test simulates agents, not the jobs they run (patch scans, software
  installs, configurations); those are spread over time by the scheduler.
