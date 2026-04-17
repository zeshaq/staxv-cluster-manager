# ISO Library (bare-metal OS install media)

Shipped commit `c8ff20c`. The catalog of .iso install images an admin
uploads (or imports from a mirror URL), used by BMC Virtual Media to
install an OS onto an enrolled physical server.

## Scope (shipped)

### API (admin-only, auth required)
- `GET    /api/isos` — list, newest first.
- `GET    /api/isos/{id}` — detail.
- `POST   /api/isos/upload` — multipart, streams to disk while hashing. Form fields (order matters — we walk parts sequentially): `name`, `os_type`, `os_version`, `description`, then `file`.
- `POST   /api/isos/import` — body `{url, name, filename?, os_type, os_version, description}`. Returns 202 immediately with status=`downloading`. Background goroutine runs on `context.Background()` with `cfg.ISOs.DownloadTimeout` (default 2h).
- `DELETE /api/isos/{id}` — cancels in-flight downloads via per-id `CancelFunc` map, removes file. Idempotent.

### BMC-facing serve (NO auth)
- `GET /iso/{id}/{filename}` — this is the URL a BMC fetches via Redfish `VirtualMedia.InsertMedia`. BMCs can't carry our session cookie.
- Filename in path MUST match the DB row (`iso.filename`) — otherwise 404. Prevents id-probing from leaking the catalog.
- Uses `http.ServeContent` so Range requests work (BMC partial-mount resume).
- Status must be `ready` — `downloading`/`error` rows 409.
- **Protection model**: network segmentation. In prod, the BMC management LAN is isolated. For now, anyone on that LAN who knows both id and filename can fetch. Tokenized URLs (short-lived HMAC) are a future v2 concern.

### Frontend
- Sidebar entry "ISOs" (Disc3 icon) between Fleet and Users.
- `pages/ISOs.jsx` with two-tab Add panel:
  - **Upload**: axios `onUploadProgress` drives a progress bar. Click-anywhere drop zone.
  - **Import from URL**: fire-and-forget, closes on success. Auto-poll every 3 s refreshes the list while any row is `uploading`/`downloading`.
- Per-ISO card: OS-typed icon (Terminal/Monitor/FileCog for linux/windows/esxi), size, SHA256 prefix (hover for full), source URL, description, error banner.
- **Copy URL** button on `ready` rows — gives admin the BMC-facing absolute URL to paste into iLO/iDRAC Virtual Media UI today (automatic mounting is a follow-up).

## Data model (`0005_isos.sql`)

```
isos(
  id PK,
  name,                   -- display name
  filename UNIQUE,        -- basename on disk; UNIQUE prevents silent overwrite
  path,                   -- absolute; <root>/<filename>
  size_bytes,             -- 0 while in-flight
  sha256?,                -- NULL until ready
  os_type default 'linux', -- linux | windows | esxi | other
  os_version?, description?, source_url?,
  status default 'uploading', -- uploading | downloading | ready | error
  error?,
  uploaded_by? FK users(id) ON DELETE SET NULL,
  created_at, updated_at
)
indexed by status + os_type.
```

No `owner_id` — OS install is admin-only globally. Same reasoning as
the servers table.

## Filesystem layer (`internal/isolib`)

- `SaveUpload(part, destName)` — streams a multipart.Part to disk, computes SHA256 on the fly via `io.MultiWriter(file, hasher)`.
- `Download(ctx, url, destName)` — same for URL imports. Sets User-Agent so polite mirrors don't reject.
- `Open(path)` — opens for serving, with root-containment check.
- `Remove(path)` — idempotent, root-containment check (defense-in-depth against a tampered DB row telling us to unlink `/etc/passwd`).
- All opens use `O_EXCL` — refuse to silently overwrite a file a BMC might already have mounted. Re-upload with the same filename → 409.
- Extension check: only `.iso` (hypervisor's sister lib takes qcow2/raw/img; those are hypervisor-side formats, not BMC-mountable).

## Config (`[isos]`)

```toml
[isos]
path = "./tmp/isos"          # prod: /var/lib/staxv-cluster-manager/isos
max_upload_gb = 20
download_timeout = "2h"      # ESXi ISOs from VMware mirrors can crawl
```

Defaults work in dev — dir auto-created 0755 on first boot.

## Router wiring gotcha

The default 60 s `middleware.Timeout` was **moved into a `chi.Group`**
so ISO routes skip it. A 4 GB upload at 100 Mbps takes ~5 min; serving
a multi-GB ISO to a BMC over 1 Gbps takes ~30 s; both would die
mid-flight under the global 60 s cap. See `cmd/staxv-cluster-manager/main.go`
— the ISO handler is mounted **outside** the timeout group.

## Smoke-test evidence (end-to-end, dl385-2)

All five cases passed on the first run after fixing the rsync-path bug:

1. **Upload 5 MB file** → SHA256 computed on-the-fly matches original byte-for-byte. Status=`ready`.
2. **Public fetch without cookie** → HTTP 200, identical SHA256. BMC flow works.
3. **Wrong filename in URL** → HTTP 404. Filename match enforced.
4. **Range request** (`Range: bytes=0-1023`) → HTTP 206, correct 1024 bytes. BMC partial-mount supported.
5. **URL import from local `python3 -m http.server`** → `downloading` → `ready` in < 2 s, size + sha256 populated, served byte-identical.
6. **DELETE** → HTTP 204, file removed from disk.

## Scope (deferred — obvious next commits)

- **Redfish `VirtualMedia.InsertMedia` wiring** — `POST /api/servers/{id}/mount-iso` body `{iso_id}` that calls `/redfish/v1/Managers/{id}/VirtualMedia/{slot}/Actions/VirtualMedia.InsertMedia` with the `/iso/{id}/{filename}` URL. Plus "Boot from ISO" button on server detail page that triggers mount + `POST .../Actions/ComputerSystem.Reset {"ResetType":"ForceRestart"}` + one-time boot override.
- **Tokenized serve URLs** — short-lived HMAC tokens so the BMC URL rotates per-mount; stops anyone-on-mgmt-LAN from leeching ISOs.
- **Integrity verification** — pre-mount, optionally check vendor checksums. Frontend form field for "expected SHA256" that we compare after download.
- **Curated catalog** — "import Ubuntu 24.04" one-click with a known-good URL list per distro. Lives in config, not code, so operators can pin mirror choices.
- **GC** — prune error-state rows older than N days; warn on orphan files in the library root (file on disk without a DB row).
