# Network Devices — Cisco IOS / IOS-XE (SSH)

Inventory + live health for Cisco switches and routers on the fleet's
management LAN. Sibling to `servers.md` (Redfish BMCs) but a different
domain: SSH to a CLI instead of HTTPS to Redfish, VLAN/interface world
instead of power/boot. Two parallel pages in the UI — same UX shape,
distinct data.

## Status

**Phase 1 shipped.** Enrollment, probe (`show version`), live health
(CPU / memory / environment / interfaces). Read-only.

Deferred for Phase 2/3 (planned; tracked below).

## Scope (shipped)

### CRUD + reachability
- `POST /api/network-devices` — enroll `{name, mgmt_host, mgmt_port?, username, password, enable_password?, platform?}`. SSH-probed immediately; row stored with `status = reachable | unreachable | error`.
- `GET /api/network-devices` — list every row (credentials redacted; only `has_enable: bool`).
- `GET /api/network-devices/{id}` — detail.
- `DELETE /api/network-devices/{id}` — idempotent.
- `POST /api/network-devices/{id}/test` — re-probe, update reachability + discovered fields.

### Live health (commit 1)
- `GET /api/network-devices/{id}/health` — runs in sequence:
  - `show processes cpu sorted 5sec` → 5-sec / 1-min / 5-min CPU percent
  - `show memory statistics` → Processor pool used/free bytes
  - `show environment all` → flattened [{kind, name, reading, state}] — best-effort; "% Invalid input" treated as "not supported by this box" rather than a hard error
  - `show ip interface brief` → per-interface {name, ip, method, status, protocol}
- Single SSH session per call, serialized — SSH sessions aren't safe for concurrent writes on one channel.
- Partial-failure-friendly: each block has its own `*_error` field in the JSON so the UI renders usable data alongside per-block errors.
- ~2-4s end-to-end on a warm mgmt connection; UI lazy-loads on expand.

### Offline bulk enrollment (CLI)

`staxv-cluster-manager network-add` — inserts a device directly into
SQLite without requiring the HTTP server to be running or a live
browser session. Same AEAD the server uses, so the row is
immediately usable after a restart.

```sh
staxv-cluster-manager network-add \
  --config /etc/staxv-cluster-manager/config.toml \
  --name core-router --host 192.168.111.1 \
  --username admin --password admin \
  [--port 22] [--enable ...] [--platform ios|ios-xe|nxos] [--probe]
```

- Default: row lands with `status=unknown`. Admin hits the UI's
  **Refresh** button (or `POST /api/network-devices/{id}/test`) to
  trigger the SSH probe.
- `--probe` runs the SSH probe synchronously from the CLI — useful on
  a lab operator's laptop where the mgmt network is reachable and you
  want immediate "reachable / unreachable" feedback.
- `--password -` reads the password from stdin so scripts can pipe
  without exposing the secret in `ps` output or shell history.

This is the path for labs and bulk imports. Single-device adds via
the UI remain the expected flow.

### Frontend pieces
- Sidebar entry **Network** (`Router` icon), placed between Servers and Fleet so the layering reads: "physical boxes → network fabric → hypervisors → tooling."
- `pages/Network.jsx` — card grid with inline enroll panel. StatusBadge + model/version/serial block on each card.
- `pages/NetworkDeviceDetail.jsx` — header (name, mgmt host, model, IOS version, platform, uptime pill), ID / Management / Metadata sections, and a lazy-loaded **Live health** expandable (CPU/mem stat cards with color tones, environment + interfaces SubTables).

All endpoints **admin-only**. Device credentials = full fabric control
(VLAN push, interface IP changes, `wr erase`); no sub-admin visibility.

## Security

- Login password **and** optional enable password AES-256-GCM encrypted at rest — reuses the same `pkg/secrets` AEAD + 32-byte key file as servers + settings.
- Decryption is scoped to a single SSH call. API redacts: `GET /api/network-devices[/{id}]` returns only `username` + a `has_enable` boolean; neither password crosses the wire.
- SSH host-key verification intentionally disabled (`ssh.InsecureIgnoreHostKey`) — mgmt-LAN gear rotates host keys on reloads, and a trusted-fingerprint store is a later feature. Same stance as Redfish's `InsecureSkipVerify=true`.
- Auth methods: password + keyboard-interactive (some IOS images only accept keyboard-interactive). No public-key auth yet.
- 20 s per-call timeout; 30 s for probe + health wrappers.

## Data model (`0006_network_devices.sql`)

```
network_devices(
  id PK, name UNIQUE,
  mgmt_host, mgmt_port default 22,
  username,
  password_enc,
  enable_password_enc?,
  platform default 'ios',     -- 'ios' | 'ios-xe' | 'nxos' (autodetected from show version)
  status, status_error?,
  last_seen_at?,
  model?, version?, serial?, hostname?, uptime_s?,
  created_at, updated_at
)
indexed by status + platform.
```

No `owner_id` — same reasoning as servers. Live operational readings
(CPU, memory, env, interfaces) are **NOT** cached; fetched live on each
/health call. Those change continuously and staleness would mislead
more than help.

## SSH client (`pkg/cisco`)

Deliberately in-house rather than pulling `scrapligo` — Phase 1 is
read-only `show` commands, which need ~200 lines of prompt-handling
and output parsing. Scope of library-vs-custom reopens when Phase 2
(config push: VLANs, interface IPs) lands; if enable escalation,
config-mode prompts, and multi-line `configure replace` rollback get
hairy, switching to scrapligo is a drop-in.

Files:
- `client.go` — `Dial`, `RunCommand`, `Close`. Opens an interactive PTY, matches the prompt regex (scoped to the discovered hostname), runs `terminal length 0` + `terminal width 0` on connect, optionally escalates via `enable` + password. Typed errors: `DialError` (TCP/SSH handshake) vs `AuthError` (creds rejected) so handlers can classify 503 vs 401.
- `probe.go` — `Probe()` runs `show version`, parses version / model / serial / uptime / platform-family. Regex-based + forgiving; missing fields stay empty rather than failing.
- `health.go` — `GetHealth()` runs CPU + memory + env + interfaces in sequence; each block has its own error so partial success is normal.

Prompt detection is the subtle bit: IOS doesn't give a clean
delimiter. We match against `<hostname>[(config[-…])]? [>#]\s*$` on
the last line of the buffer. The hostname is learned from the login
banner's trailing prompt.

## Scope (deferred — Phase 2+3)

Planned but not shipped. Each its own commit:

- **Phase 2 — VLAN management.**
  - `GET  /api/network-devices/{id}/vlans` — parse `show vlan brief`
  - `POST /api/network-devices/{id}/vlans` body `{id, name}` — `conf t / vlan N / name X / end / wr mem`
  - `DELETE /api/network-devices/{id}/vlans/{vlan_id}` — `conf t / no vlan N / end / wr mem`
  - UI table with inline add + delete confirm
  - Safety: snapshot `show running-config | section vlan` before + diff after; log both
- **Phase 3 — Interface IP management.**
  - `GET /api/network-devices/{id}/interfaces` — `show interfaces` + `show ip interface` for each; richer than the brief used in health
  - `PUT /api/network-devices/{id}/interfaces/{iface}` body `{ip, mask}` or `{clear: true}` — `conf t / interface X / [no] ip address … / no shut / end / wr mem`
  - UI edit-in-place per row
- **Topology integration (optional)** — parse `show cdp neighbors detail` / `show lldp neighbors detail`, correlate MAC/IP to rows in `servers` table, show "server eth0 → switch1 Gi1/0/5" on the Servers page.
- **Config backup** — periodic (cron-style goroutine) `show running-config` → versioned store (git-backed or DB table with history).
- **OEM protocols where they buy us something** — NX-API for Nexus (structured JSON beats regex-parsing `show` output), RESTCONF/NETCONF on IOS-XE for transactional edits.
- **SSH key auth + host-key trust store.**
- **Periodic reachability checks** — background goroutine, same idea as the deferred server-side feature.

## Why a separate domain from servers

Tempting to shove switches into the `servers` table with a "kind"
column, but the fields diverge too fast: servers have `power_state` +
`health` + `memory_gb` + `cpu_count`; network devices have `platform`
+ `uptime_s` + `model` (and will grow VLANs + interfaces). Separate
tables keep each schema honest. Shared: the AEAD key, the admin-only
gating, and the UX shape.
