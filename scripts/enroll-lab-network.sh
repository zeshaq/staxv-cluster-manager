#!/usr/bin/env bash
#
# enroll-lab-network.sh — bulk-enroll the lab's Cisco switches and core
# router into the cluster-manager DB. One-shot; run on the
# cluster-manager host where the SQLite DB + secrets key live.
#
# CREDENTIALS ARE HERE IN PLAINTEXT — LAB ONLY.
# `admin/admin` on every device is indefensible outside a lab.
# Rotate before anything leaves that network.
#
# After running: hit Refresh in the UI (or POST /{id}/test) to probe
# each device over SSH. Pass --probe when running this script if the
# cluster-manager host can already reach the mgmt IPs and you want
# reachability feedback inline.

set -euo pipefail

CM="${CM:-./staxv-cluster-manager}"                 # path to the binary (overridable)
CONFIG="${CONFIG:-/etc/staxv-cluster-manager/config.toml}"
PROBE_FLAG=""
if [[ "${1:-}" == "--probe" ]]; then
    PROBE_FLAG="--probe"
    echo "→ running with --probe (will SSH-probe each device)"
fi

enroll() {
    local name="$1" host="$2" username="$3" password="$4"
    echo
    echo "== $name ($host) =="
    # Use --password - so the secret doesn't sit in `ps auxf`.
    printf '%s\n' "$password" | \
      "$CM" network-add \
        --config "$CONFIG" \
        --name "$name" --host "$host" \
        --username "$username" --password - \
        $PROBE_FLAG
}

# Core router — one device, four interfaces. Enrolling the internal
# mgmt IP only; public IPs (103.108.63.113, 59.153.29.97, 52.52.0.1)
# intentionally skipped.
enroll "core-router"   "192.168.111.1" "admin" "admin"

# CTO sir's room switch
enroll "cto-switch"    "12.12.254.254" "admin" "admin"

# Datacenter switches
enroll "core-switch"   "192.168.5.200" "admin" "admin"
enroll "rack-1-switch" "192.168.5.201" "admin" "admin"
enroll "rack-2-switch" "192.168.5.202" "admin" "admin"
enroll "rack-3-switch" "192.168.5.203" "admin" "admin"
enroll "rack-4-switch" "192.168.5.204" "admin" "admin"

echo
echo "done. open /network in the UI to verify; click Refresh on any row"
echo "if you didn't pass --probe."
