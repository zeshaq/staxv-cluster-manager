-- 0007_network_device_role.sql — classify network devices by role.
--
-- Rather than splitting switches + routers into separate tables (which
-- would force an awkward "where do L3 switches go?" decision and
-- duplicate the SSH-plumbing layer), we keep one `network_devices`
-- table and add a role column. Admin sets at enroll time, or
-- autodetected from `show version` model string.
--
-- Enum-ish:  'router' | 'switch' | 'l3-switch' | 'firewall' | 'other' | 'unknown'
--   router     — pure L3 (ISR, ASR, C8xxx)
--   switch     — pure L2 (C2960, C9200L)
--   l3-switch  — routed-switch (C3750X, C9300, C9500, Nexus)
--   firewall   — ASA / Firepower (reserved; no handler support yet)
--   other      — admin-picked escape hatch
--   unknown    — default for unclassified rows until probe/admin fills it
--
-- CHECK constraint intentionally omitted — SQLite enforces poorly and
-- the application layer gates writes. Adding later is a migration.

ALTER TABLE network_devices
    ADD COLUMN role TEXT NOT NULL DEFAULT 'unknown';

CREATE INDEX idx_network_devices_role ON network_devices(role);
