-- 0006_network_devices.sql — Cisco IOS / IOS-XE routers + switches.
--
-- Separate from `servers` (Redfish BMCs) because the protocol, the
-- shape of the data, and the set of operations are all different —
-- network devices get VLAN / interface / routing state that physical
-- servers don't have. Sharing one table would be a lie.
--
-- Login password is AES-256-GCM encrypted at rest using the same key
-- as BMC creds (pkg/secrets). Optional enable password (for gear that
-- requires priv-15 escalation after login) gets the same treatment.
--
-- No owner_id: fleet network fabric is admin-only. Per-device ACLs
-- land in their own table if/when a multi-team need shows up.

CREATE TABLE network_devices (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    name                     TEXT    NOT NULL UNIQUE,         -- admin-chosen friendly name
    mgmt_host                TEXT    NOT NULL,                -- IP or DNS name
    mgmt_port                INTEGER NOT NULL DEFAULT 22,     -- SSH
    username                 TEXT    NOT NULL,
    password_enc             BLOB    NOT NULL,                -- AES-GCM (nonce||ct||tag)
    enable_password_enc      BLOB,                            -- NULL if priv-15 on login

    -- Platform family — influences parser selection ("ios" is the
    -- classic CLI; "ios-xe" adds RESTCONF/NETCONF possibilities;
    -- "nxos" would want NX-API). Autodetected from `show version`
    -- output on probe; admin can override via PATCH later.
    platform                 TEXT    NOT NULL DEFAULT 'ios',

    -- Reachability state — updated on enroll + every /test.
    status                   TEXT    NOT NULL DEFAULT 'unknown', -- unknown | reachable | unreachable | error
    status_error             TEXT,
    last_seen_at             DATETIME,

    -- Discovered from `show version`. NULL until first successful probe.
    model                    TEXT,
    version                  TEXT,  -- IOS version string, e.g. "15.2(4)E7"
    serial                   TEXT,
    hostname                 TEXT,  -- device's configured hostname
    uptime_s                 INTEGER, -- seconds

    created_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_network_devices_status   ON network_devices(status);
CREATE INDEX idx_network_devices_platform ON network_devices(platform);
