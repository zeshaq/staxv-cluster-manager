-- 0004_servers_detail.sql — richer Redfish fields for the detail page.
--
-- Each is NULLable; backfilled on the next probe. No data migration
-- required — existing rows just show "—" in the UI until their next
-- /test runs.
--
-- Sources (all from `/redfish/v1/Systems/{id}`, same doc we already hit):
--   power_state   ← PowerState            ("On" | "Off" | "PoweringOn" | "PoweringOff" | "Unknown")
--   health        ← Status.Health         ("OK" | "Warning" | "Critical")
--   bios_version  ← BiosVersion
--   hostname      ← HostName              (set only if OS reports one back to BMC)
--   cpu_count     ← ProcessorSummary.Count
--   memory_gb     ← MemorySummary.TotalSystemMemoryGiB

ALTER TABLE servers ADD COLUMN power_state  TEXT;
ALTER TABLE servers ADD COLUMN health       TEXT;
ALTER TABLE servers ADD COLUMN bios_version TEXT;
ALTER TABLE servers ADD COLUMN hostname     TEXT;
ALTER TABLE servers ADD COLUMN cpu_count    INTEGER;
ALTER TABLE servers ADD COLUMN memory_gb    INTEGER;
