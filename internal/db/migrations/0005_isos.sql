-- 0005_isos.sql — ISO library for bare-metal OS install.
--
-- ISOs are uploaded (multipart) or imported by URL (background download)
-- and stored on the cluster-manager host filesystem. They're served
-- without auth on a public /iso/{id}/... route so a BMC's Virtual Media
-- client can fetch them via Redfish Virtual Media Insert — BMCs don't
-- speak cookie auth and signing URLs per-mount is a v2 concern.
--
-- Scope: admin-only, global catalog. No owner_id — server OS install
-- is a privileged operation shared across all admins, unlike hypervisor
-- ISOs which are per-user.
--
-- SHA256 is computed on upload / download completion. Empty until
-- status='ready'. Useful for "did the upload complete cleanly?" and
-- for future integrity checks before attaching to a server.

CREATE TABLE isos (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL,                 -- human-friendly display name (e.g. "Ubuntu 24.04 LTS")
    filename        TEXT    NOT NULL UNIQUE,          -- basename on disk; unique prevents silent overwrite
    path            TEXT    NOT NULL,                 -- absolute path (derived: <root>/<filename>)
    size_bytes      INTEGER NOT NULL DEFAULT 0,       -- 0 while downloading; real size after ready
    sha256          TEXT,                             -- hex, computed on completion (nullable until then)

    os_type         TEXT    NOT NULL DEFAULT 'linux', -- linux | windows | esxi | other
    os_version      TEXT,                             -- free-form: "24.04", "2022", "8.0U3", ...
    description     TEXT,
    source_url      TEXT,                             -- non-empty when imported from URL

    -- Lifecycle:
    --   uploading  — multipart upload in progress
    --   downloading — HTTP fetch in progress (source_url set)
    --   ready      — file exists, sha256 populated
    --   error      — last attempt failed; `error` has details
    status          TEXT    NOT NULL DEFAULT 'uploading',
    error           TEXT,

    uploaded_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,

    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_isos_status  ON isos(status);
CREATE INDEX idx_isos_os_type ON isos(os_type);
