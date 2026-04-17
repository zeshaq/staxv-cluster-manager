-- 0003_servers.sql — physical server inventory via Redfish (iLO / iDRAC).
--
-- BMC credentials are AES-256-GCM encrypted at rest using the same
-- key as settings (see pkg/secrets). Decrypted only in memory when
-- the handler needs to dial Redfish.
--
-- No owner_id column: physical-server management is admin-only for
-- now. A future "who can see what" policy gets its own access table
-- rather than piggy-backing on a column here.

CREATE TABLE servers (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT    NOT NULL UNIQUE,            -- admin-chosen friendly name
    bmc_host            TEXT    NOT NULL,                   -- IP or DNS name of the BMC (iLO/iDRAC)
    bmc_port            INTEGER NOT NULL DEFAULT 443,       -- HTTPS by default
    bmc_username        TEXT    NOT NULL,
    bmc_password_enc    BLOB    NOT NULL,                   -- AES-GCM (nonce||ct||tag), format matches pkg/secrets

    -- Fields discovered from Redfish on successful enroll/test.
    -- NULL until the first reachable check populates them.
    manufacturer        TEXT,                               -- "HPE", "Dell Inc.", "Lenovo", ...
    model               TEXT,                               -- "ProLiant DL385 Gen10", "PowerEdge R740", ...
    serial              TEXT,                               -- vendor serial / service tag

    -- Reachability state — updated on enroll + every /test call.
    status              TEXT    NOT NULL DEFAULT 'unknown', -- unknown | reachable | unreachable | error
    status_error        TEXT,                               -- last non-nil error message
    last_seen_at        DATETIME,                           -- last successful Redfish call

    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_servers_status ON servers(status);
