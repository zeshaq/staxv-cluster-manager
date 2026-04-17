# Network Devices — Cisco IOS / IOS-XE (SSH)

Inventory + live health for Cisco switches and routers on the fleet's
management LAN. Sibling to `servers.md` (Redfish BMCs) but a different
domain: SSH to a CLI instead of HTTPS to Redfish, VLAN/interface world
instead of power/boot. Two parallel pages in the UI — same UX shape,
distinct data.

## Status

**Phase 1 shipped** (commit `659a415`). Enrollment, probe (`show
version`), live health. Read-only.

**Phase 1.5 shipped** (commit `cad8681`). Role classification —
router / switch / l3-switch / firewall / other / unknown. Autodetect
from `show version` model string with admin-override sticky.

**Phase 2 shipped.** VLAN management — list, create, delete. UI
gated to role ∈ {switch, l3-switch}.

**Phase 3 shipped.** Interface IP management — list, set, clear.
UI gated to role ∈ {router, l3-switch}.

**Phase 4 shipped.** OSPF — process CRUD, per-interface attach with
optional `network point-to-point` for /30 WAN links, read-only
neighbor table. UI gated to role ∈ {router, l3-switch}.

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

### Role classification (Phase 1.5)

Rather than splitting the table into routers + switches, one
`network_devices` table with a `role` column. Cisco itself blurs
the line (L3 switches speak both languages); separate tables would
force an awkward classification at enroll time and duplicate 80%+
of the SSH plumbing.

Enum (validated at the handler layer; no SQL CHECK constraint):

| Value       | Devices                                          |
|-------------|--------------------------------------------------|
| `router`    | ISR / ASR / C8xxx / classic 19xx-39xx / RV       |
| `l3-switch` | C3xxx / C9300 / C9400 / C9500 / Nexus            |
| `switch`    | C2960 / C9200(L) / SG / SF                       |
| `firewall`  | ASA / Firepower (no handler support yet)         |
| `other`     | Admin-picked escape hatch                        |
| `unknown`   | Default until autodetect or admin sets it        |

- **Autodetect** at enrollment only — `pkg/cisco.DetectRole(model)`
  runs after the probe returns `show version`'s parsed model string.
  Uses prefix match + a regex for the 1900/2900/3900 ISR G2 family
  (`[123]9\d{2}(/K9)?`). Unit-tested in `pkg/cisco/role_test.go`;
  new SKUs land as new cases + detection updates.
- **Admin override** via `POST /api/network-devices/{id}/role` body
  `{"role": "..."}`. Sticky — `SetRoleIfUnknown` is the enrollment-
  path write, so subsequent re-probes don't clobber an admin pick.
  To re-run autodetect, admin sets role=unknown and re-enrolls /
  probes (a dedicated `/redetect` endpoint is a follow-up if needed).
- **Frontend** — role badge on every card (icon + color per role),
  filter bar on the list page (`All · Routers · L3 switches ·
  Switches · …`) that only shows tabs for roles actually present in
  the fleet. Detail page has an inline-edit dropdown in the header
  + in the Identification section.
- **Phase 2/3 scoping** — the VLAN editor will be visible on
  `switch` + `l3-switch`; the interface-IP editor on `router` +
  `l3-switch`; the routing-table viewer (later) on `router` +
  `l3-switch`. Keeps feature surfaces appropriate per device class.

Migration: `0007_network_device_role.sql` — `ADD COLUMN role TEXT
NOT NULL DEFAULT 'unknown'` + index.

### VLAN management (Phase 2)

Read + single-VLAN write. Bulk / range syntax deferred.

- `GET    /api/network-devices/{id}/vlans` — parses `show vlan brief` → `[{id, name, status, ports[]}]`. Empty slice on devices without VLAN support (we don't 404; some pure routers legitimately reject the command).
- `POST   /api/network-devices/{id}/vlans` body `{id, name}` — validates ID 1-4094 (rejecting 1002-1005 reserved range) + name regex (1-32 chars, alnum + hyphen + underscore), then runs `configure terminal / vlan N / name X / exit / end / write memory`. Response: updated list + `config_before` + `config_after` running-config snapshots.
- `DELETE /api/network-devices/{id}/vlans/{vlan_id}` — rejects VLAN 1 (the IOS default) client-side for a cleaner error; otherwise runs `no vlan N / end / wr mem`. Idempotent (IOS silently tolerates deleting a missing VLAN). Same response shape with snapshots.

**Config-mode machinery** lives in `pkg/cisco/config.go`:
- `RunConfigLines(lines, save bool)` — enters config mode, sends each line, detects IOS error markers (`% Invalid input`, `% Ambiguous command`, etc.) via `HasIOSError`, bails on first error while still sending `end` to leave config sub-modes cleanly, optionally runs `write memory`. No transactional rollback — classic IOS doesn't support it. Per-line replies returned for debugging.
- `ShowRunningConfig(section)` — runs `show running-config | section <name>` (or full config when empty). Used by VLAN write methods to snapshot before/after for audit.

**VLAN-layer** in `pkg/cisco/vlans.go`:
- `VLAN` type: `{id, name, status, ports[]}`.
- `VLANs()` — list, parses `show vlan brief` with continuation-line handling (IOS wraps long port lists across whitespace-prefixed lines with no ID; parser collapses them back onto the current row).
- `CreateVLAN(id, name)` / `DeleteVLAN(id)` — return (before, after, err). `before` is captured even on error so the admin can see what state the device was in before the failed write.
- `VLANNameRE` / `VLANIDMin` / `VLANIDMax` / `VLANIDRsvdMin` / `VLANIDRsvdMax` — validation constants shared with the handler.

**Audit trail** — every write logs before + after running-config snapshots via `slog.Info`. On failure, `slog.Warn` captures the same plus the error. Not yet a DB-backed audit log (deferred — a `network_device_changes` table with `{timestamp, actor, device_id, op, before_config, after_config}` is the clean shape).

**Role-gated UI** — the `VLANSection` component on the device detail page only renders when `device.role ∈ {switch, l3-switch}`. Pure routers don't get the section (they can still manage VLANs via CLI; this just reflects the common case). Inside the section: inline add-VLAN form with client-side validation mirroring the backend's regex + ID range, per-row delete with confirm, collapsible "Last change" revealing before/after config snapshots from the response.

### Interface IP management (Phase 3)

Read + single-interface write. One CIDR per interface; the edit
field carries both host + mask, which the backend splits into IOS's
`ip address A.B.C.D M.M.M.M` form.

- `GET  /api/network-devices/{id}/interfaces` — merges `show ip interface brief` with `show interfaces description`. Returns `[{name, description, ip, method, status, protocol}]`. `description` is best-effort — older IOS that rejects `show interfaces description` just leaves it empty.
- `POST /api/network-devices/{id}/interface-ip` body `{name, ip}` — `ip` in CIDR form (`"10.1.1.1/24"`) OR empty string to clear. Validates name regex (`^[A-Za-z][A-Za-z0-9/.\-]+$`) at the handler, then runs:
  - **Set**: `conf t / interface X / ip address A.B.C.D M.M.M.M / no shutdown / exit / end / wr mem` — `no shutdown` is automatic so admin doesn't forget it after assigning a fresh IP.
  - **Clear**: `conf t / interface X / no ip address / exit / end / wr mem` — admin state stays as-is (admin might want the interface up without an IP, e.g. for a bridge member).

**Interface-layer** in `pkg/cisco/interfaces.go`:
- `Interface` type (name, description, ip, method, status, protocol). Mask NOT included in the list view — `show ip interface brief` doesn't carry it and pulling per-interface `show ip interface` on a 48-port switch is wasteful for a simple list. The edit form accepts CIDR anyway so the admin specifies mask at write time.
- `Interfaces()` — list via the two commands mentioned above.
- `SetInterfaceIP(name, cidr)` / `ClearInterfaceIP(name)` — return (before, after, err). `before` captured even on error.
- `cidrToIOS(cidr)` — splits "10.1.1.1/24" → ("10.1.1.1", "255.255.255.0"). IPv4-only; rejects `/0`, `/33+`, IPv6, invalid shapes. Unit-tested.
- `InterfaceNameRE` — server-side validation of names before we interpolate into config lines. Avoids any shell-injection-ish risks from typed names.

**Config-mode reuse** — `RunConfigLines` + `ShowRunningConfig(section)` from Phase 2's config.go handle the low-level plumbing. Phase 3 just wires them to interface-specific commands.

**Role-gated UI** — `InterfaceSection` on the device detail page only renders when `device.role ∈ {router, l3-switch}`. Pure L2 access switches rarely have per-port IPs (they use a management SVI); surfacing this editor on them would be confusing. Admin can still do it via CLI. Inside the section: lazy-loaded table with inline-edit pencil button per row — click opens a CIDR input pre-filled with the current IP + `/24` default, Enter to save, Escape to cancel. Blank input + Save clears the IP. Collapsible "Last change" at the bottom reveals before/after `show run | section interface <name>` snapshots.

### OSPF (Phase 4)

Enough to stand up a small P2P mesh between lab routers without
dropping to CLI: enable a process, assign interfaces, flip `network
point-to-point` so /30 links form neighbors without DR/BDR
election.

- `GET    /api/network-devices/{id}/ospf` — returns `{processes[], interfaces[], neighbors[], *_error}`. Three independent queries; per-block errors surfaced so a partial failure still shows useful data.
- `POST   /api/network-devices/{id}/ospf/processes` body `{pid, router_id}` — idempotent upsert. `router ospf N / router-id X.X.X.X / end / wr mem`.
- `DELETE /api/network-devices/{id}/ospf/processes/{pid}` — `no router ospf N`. IOS auto-removes interface attachments for this pid.
- `POST   /api/network-devices/{id}/ospf/interface` body `{name, pid, area, network_type}` — full-state set. `pid=0` clears. The backend reads the current per-interface running-config first so it knows which `(pid, area)` to `no ip ospf` when transitioning, then emits the new `ip ospf N area A` + optional `ip ospf network <type>`.

**OSPF-layer** in `pkg/cisco/ospf.go`:
- Types: `OSPFProcess {pid, router_id, areas[]}`, `OSPFInterface {name, pid, area, ip_cidr, cost, state, neighbors_fc, network_type}`, `OSPFNeighbor {neighbor_id, priority, state, dead_time, address, interface}`. Bundled via `OSPFState`.
- `GetOSPFState()` runs `show running-config | section ^router ospf` (structured — beats parsing `show ip ospf`'s variable format) + `show ip ospf interface brief` + `show ip ospf neighbor`. Areas per process are derived from the interface attachments table.
- `CreateOrUpdateOSPFProcess(pid, routerID)` / `DeleteOSPFProcess(pid)` / `SetOSPFInterface(name, pid, area, networkType)` — each returns (before, after, err) snapshots.
- `parseInterfaceOSPFFromConfig` extracts `(pid, area, network_type)` from a per-interface running-config block so we know the current state before a transition.
- `validateRouterID` requires dotted-decimal IPv4.
- `OSPFNetworkTypes` — allow-list map: `point-to-point` | `point-to-multipoint` | `broadcast` | `non-broadcast` | `""` (= don't touch).

**Transition logic** in `SetOSPFInterface` (the tricky bit):

| Current | Desired | Commands sent |
|---|---|---|
| no OSPF | `pid=1, area=0` | `ip ospf 1 area 0` |
| no OSPF | `pid=1, area=0, p2p` | `ip ospf 1 area 0` + `ip ospf network point-to-point` |
| `pid=1, area=0` | `pid=0` (clear) | `no ip ospf 1 area 0` |
| `pid=1, area=0, p2p` | `pid=0` | `no ip ospf 1 area 0` + `no ip ospf network` |
| `pid=1, area=0` | `pid=1, area=5` | `no ip ospf 1 area 0` + `ip ospf 1 area 5` |
| `pid=1, area=0` | `pid=2, area=0` | `no ip ospf 1 area 0` + `ip ospf 2 area 0` |

**Role-gated UI** — `OSPFSection` on device detail, renders only when `device.role ∈ {router, l3-switch}`. Three sub-tables (Processes / Interfaces / Neighbors) plus:
- Add-process form (inline, PID + Router ID validation mirroring backend).
- Per-interface inline edit (PID / Area / network-type dropdown). Non-attached interfaces appear with PID=0 and are editable to attach.
- Neighbor states colored by progress: green for FULL/*, amber for 2WAY, gray for DOWN/INIT.
- "Last change" collapsible with before/after config snapshots from the response.

**Scope (deferred within OSPF)** — cost / hello-interval / dead-interval / priority / passive-interface / authentication / stub-area / virtual-links / LSDB viewer. Per-interface cost is a one-line addition if / when needed; the auth + area-level stuff is more substantial.

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
