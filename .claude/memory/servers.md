# Physical Servers — Redfish (iLO / iDRAC)

Inventory of the bare-metal boxes the hypervisors run on. Cluster-manager
talks to their BMCs over Redfish; hypervisors themselves live in the
Fleet page.

This is what in vCenter would be the "Hosts" pane crossed with
"Hardware Status" — we talk straight to the BMC, not through an ESXi/
hypervisor agent.

## Scope (shipped)

### CRUD + reachability
- `POST /api/servers` — enroll by `{name, bmc_host, bmc_port, bmc_username, bmc_password}`. Server immediately probed; row stored with `status = reachable | unreachable | error`.
- `GET /api/servers` — list every row.
- `GET /api/servers/{id}` — detail.
- `DELETE /api/servers/{id}` — idempotent.
- `POST /api/servers/{id}/test` — re-probe, update reachability + discovered fields.

### Power control
- `POST /api/servers/{id}/power` body `{"action": "<name>"}` — valid names: `on`, `shutdown` (graceful), `reboot` (graceful), `force_off`, `force_reboot`, `power_cycle`, `nmi`, `push_button`.
- Flow: walks Systems → reads Actions.Reset.target (with spec-compliant fallback) → POSTs ResetType. Pre-flights against BMC's advertised `AllowableValues`. Re-probes after success so the response carries fresh `power_state`.
- Frontend: five power-action chips on detail page; enable/disable logic keyed to current PowerState ("Power On" disabled when On; destructive actions disabled when Off). Danger variants (Force Off / Force Restart) require `confirm()`.

### Hardware inventory (commit 5a16c32)
- `GET /api/servers/{id}/hardware` — CPUs, DIMMs, drives, NICs. Each collection fetched concurrently; per-collection errors surface in the JSON so a partial-failure BMC still yields useful data.
- `pkg/redfish/hardware.go`:
  - `Processors()` — `/Systems/{id}/Processors` → per-socket fan-out (cap 8 in-flight).
  - `Memory()` — `/Systems/{id}/Memory` → per-DIMM-slot (populated + empty).
  - `Drives()` — `/Systems/{id}/Storage` → per-controller → flat drive list with parent controller name.
  - `NetworkInterfaces()` — `/Systems/{id}/EthernetInterfaces`.
- Empty slots (`capacity_mib = 0` on memory) are filtered client-side; the raw list includes them so admin CAN see "slot N empty" if they want.

### Thermal + power sensors (commit 5a16c32)
- `GET /api/servers/{id}/health` — fans, temperature probes, PSUs, live power consumption.
- `pkg/redfish/health.go`:
  - `Thermal()` — `/Chassis/{id}/Thermal`. Single GET (fans + temps are inline arrays).
  - `Power()` — `/Chassis/{id}/Power`. PSU inventory + `PowerControl[0]` for aggregate consumption (ConsumedWatts, PowerMetrics.Average/Min/Max).
- Temps colored yellow when ≥ UpperThresholdNonCritical, red when ≥ UpperThresholdCritical. Stats row shows current / avg / peak / capacity watts.

### Frontend pieces
- `/servers` list: inline enroll form, card grid, test/delete actions, status badges.
- `/servers/:id` detail page: ID card (manufacturer/model/serial/BIOS/hostname), Hardware card (CPU/memory/power-state/health), BMC card, Metadata card.
- Power actions section with five chips.
- **Hardware inventory** expandable section — lazy-loads on first expand (no preload), caches in memory, reload button in header. Renders four SubTables (Processors, Memory, Drives, NICs) with shared dense-table styling.
- **Thermal & power** expandable section — same lazy-load pattern. Stats row (current/avg/peak/capacity watts) above three SubTables (Fans, Temperatures, PSUs).
- Shared helpers in `ServerDetail.jsx`: `ExpandableSection`, `SubTable`, `Pip` (health color chip), `fmtBytes`, `fmtMiB`, `Stat`.

All endpoints **admin-only**. BMC creds = full physical control of the
box (power, boot media, serial) — no sub-admin visibility.

## Real-hardware smoke-test snapshot

Against a DL385 Gen11 iLO6 (the dev host's own BMC, `26.26.100.10`):
- 2× AMD EPYC 9754 128-core → 512 threads total
- 21 of 24 DIMM slots populated (64 GiB Samsung DDR5-4800)
- 8 drives, 4 NICs
- 6 fans, 49 temperature sensors, 4 PSUs
- ~410 W current draw / 3.2 kW capacity

`/hardware` takes ~24 s on this box (24 DIMMs × per-member GET, cap 8
in-flight); `/health` ~3 s (single GET per block). Acceptable for
lazy-load-on-expand UX; don't preload.

## Scope (deferred)

Each its own follow-up commit:

- **Boot media attach (Virtual Media)** — next obvious step, wires to the new `/iso/{id}/{filename}` serve URL. Redfish `VirtualMedia.InsertMedia` with Image URL. Adds "Boot from ISO" button on server detail.
- Firmware inventory + BIOS/iLO/iDRAC update workflows.
- Serial-over-LAN console proxy via Redfish.
- OEM extensions (HPE iLO `/Oem/Hpe/*`, Dell iDRAC `/Oem/Dell/*`) — only if we hit something the spec doesn't cover.
- SNMP event receiver for out-of-band BMC alerts.
- Edit creds (`PATCH /api/servers/{id}` with rotatable password).
- Scheduled periodic reachability checks (background goroutine every N minutes) instead of manual `/test`.
- Per-server ACL table (if multi-tenant physical management ever becomes a need).

## Security

- BMC password AES-256-GCM encrypted at rest — reuses the `pkg/secrets` AEAD and the same 32-byte key file as the settings store.
- Decryption happens only inside `GetCredentials` and is scoped to a single Redfish call.
- `bmc_password_enc` is never returned over the API. `GET /api/servers` redacts to username only.
- TLS: `InsecureSkipVerify=true` on the Redfish HTTP client — BMCs universally ship self-signed certs. Operators wanting proper validation would need a future `[redfish] verify_tls` knob and trusted cert store.

## Data model (0003_servers.sql + 0004_servers_detail.sql)

`servers(id PK, name UNIQUE, bmc_host, bmc_port default 443, bmc_username, bmc_password_enc, manufacturer?, model?, serial?, status, status_error?, last_seen_at?, power_state?, health?, bios_version?, hostname?, cpu_count?, memory_gb?, created_at, updated_at)`.

- `name` is admin-chosen and unique — the display label.
- `manufacturer/model/serial/bios_version/hostname` discovered from Redfish `/Systems/{id}`. NULL until a successful probe runs. Subsequent probes only update when the new value is non-empty via `COALESCE(NULLIF(?, ''), col)` (don't clobber known-good data with half-responses).
- `power_state` is the exception — always overwritten (On → Off is a legitimate transition).
- `cpu_count / memory_gb` same COALESCE pattern using `NULLIF(?, 0)`.
- `status` ∈ `{unknown, reachable, unreachable, error}`. "unreachable" = network/TLS failure; "error" = reached-but-bad (auth fail, invalid response).

Full hardware inventory (per-socket CPUs, per-slot DIMMs, etc.) and
thermal/power readings are **NOT cached in the DB** — fetched live from
the BMC on demand. That data changes (temperatures, power draw), isn't
worth stale cache risk, and the admin is already expanding a
lazy-loaded section that implies "now".

## Redfish client (`pkg/redfish`)

- `client.go` — Client, service-root / Systems-collection / Chassis-collection walks (`firstSystemURL`, `firstChassisURL`), `Probe()`, `PowerAction()`. Typed errors (`NetError`, `AuthError`, `HTTPError`) so the handler can classify reachability vs config mistake via `errors.As`.
- `hardware.go` — Processors/Memory/Drives/NetworkInterfaces; bounded-concurrency fan-out via `fetchMembers(..., cap 8)`.
- `health.go` — Thermal (fans + temps) + Power (PSUs + consumption).
- 15 s per-call timeout — BMCs are slow, especially on first SSL handshake.
- HTTP Basic auth — what Redfish mandates.
- Trailing-slash normalization on `@odata.id` members (HPE iLO trails `/`, Dell iDRAC doesn't) — caught in smoke test, fixed in `firstSystemURL`/`firstChassisURL`.

## Why no `owner_id`

Physical-server management is admin-only by design. A per-server ACL
table would be added if/when there's a need — not piggy-backed on a
column here. Keeps the schema honest about the model.
