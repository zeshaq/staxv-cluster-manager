# Physical Servers — Redfish (iLO / iDRAC)

Inventory of the bare-metal boxes the hypervisors run on. Cluster-manager
talks to their BMCs over Redfish; hypervisors themselves live in the
Fleet page.

This is what in vCenter would be the "Hosts" pane crossed with
"Hardware Status" — we talk straight to the BMC, not through an ESXi/
hypervisor agent.

## Scope (shipped)

- `POST /api/servers` — enroll by `{name, bmc_host, bmc_port, bmc_username, bmc_password}`. Server immediately probed; row stored with `status = reachable | unreachable | error`.
- `GET /api/servers` — list every row.
- `GET /api/servers/{id}` — detail.
- `DELETE /api/servers/{id}` — idempotent.
- `POST /api/servers/{id}/test` — re-probe, update reachability + discovered fields.
- Frontend `/servers` page: inline enroll form, card grid, test/delete per-card actions, status badges.

All endpoints **admin-only**. BMC creds = full physical control of the
box (power, boot media, serial) — no sub-admin visibility.

## Scope (deferred)

Each its own follow-up commit:

- Power control: `POST /api/servers/{id}/power/{on|off|reset|force-off|graceful-shutdown}`. Redfish `/redfish/v1/Systems/{id}/Actions/ComputerSystem.Reset`.
- Hardware inventory: CPU / memory / disks / NICs — real drill-down page. Redfish `/Processors`, `/Memory`, `/Storage`, `/EthernetInterfaces`.
- Sensors / thermal: fans, temperatures, power draw. Redfish `/Chassis/{id}/Thermal` + `/Power`.
- Firmware inventory + BIOS/iLO/iDRAC update workflows.
- Boot media attach (virtual CD/USB) — especially useful for "install hypervisor" flow.
- Serial-over-LAN console proxy via Redfish.
- OEM extensions (HPE iLO `/Oem/Hpe/*`, Dell iDRAC `/Oem/Dell/*`) — only if we hit something the spec doesn't cover.
- SNMP event receiver for out-of-band BMC alerts.
- Edit creds (`PATCH /api/servers/{id}` with rotatable password).
- Scheduled periodic reachability checks (background goroutine every N minutes) instead of manual `/test`.

## Security

- BMC password AES-256-GCM encrypted at rest — reuses the `pkg/secrets` AEAD and the same 32-byte key file as the settings store.
- Decryption happens only inside `GetCredentials` and is scoped to a single Redfish call.
- `bmc_password_enc` is never returned over the API. `GET /api/servers` redacts to username only.
- TLS: `InsecureSkipVerify=true` on the Redfish HTTP client — BMCs universally ship self-signed certs. Operators wanting proper validation would need a future `[redfish] verify_tls` knob and trusted cert store.

## Data model (0003_servers.sql)

`servers(id PK, name UNIQUE, bmc_host, bmc_port default 443, bmc_username, bmc_password_enc, manufacturer?, model?, serial?, status, status_error?, last_seen_at?, created_at, updated_at)`.

- `name` is admin-chosen and unique — the display label.
- `manufacturer/model/serial` are discovered from Redfish `/Systems/{id}`. NULL until a successful probe runs. Subsequent probes only update when the new value is non-empty (don't clobber known-good data with half-responses).
- `status` ∈ `{unknown, reachable, unreachable, error}`. "unreachable" = network/TLS failure; "error" = reached-but-bad (auth fail, invalid response).

## Why no `owner_id`

Physical-server management is admin-only by design. A per-server ACL
table would be added if/when there's a need — not piggy-backed on a
column here. Keeps the schema honest about the model.

## Frontend

- Sidebar nav item "Servers" with `HardDrive` icon, placed BEFORE "Fleet" to reflect the infrastructure layering (physical → hypervisor → VM).
- Home page Infrastructure section shows Physical Servers count alongside Hypervisors / VMs / Clusters — admin sees inventory at a glance.
- Enroll panel is inline (toggle on "Enroll server" button) — simpler than a modal, good enough for a form this size.
- Per-server card: status badge (lime/red/amber/slate), manufacturer/model/serial when known, test/delete actions, last_seen_at foot.

## Redfish client (`pkg/redfish`)

Minimum viable: service root + `/Systems` collection + first ComputerSystem.
- Returns typed errors (`NetError`, `AuthError`, `HTTPError`) so the handler can classify reachability vs config mistake.
- 15s per-call timeout — BMCs are SLOW, especially on first SSL handshake.
- HTTP Basic auth — what Redfish mandates.

Power ops and inventory drill-down add methods here as they arrive.

## Testing

On dl385-2 (which has no reachable BMC from this dev context): enrolled a row with a fake `192.0.2.1` → stored, marked `unreachable` with a clear error message, displayed correctly in the UI with the red status badge. Delete/test/re-enroll cycle clean.

Real-hardware test happens on first BMC the operator enrolls.
