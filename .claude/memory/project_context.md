# Project Context

## What is staxv-cluster-manager
Central control plane for a fleet of staxv-hypervisor nodes. vCenter
analog. Manages users, clusters, live migration orchestration, Redfish,
SRE.

## Relationship to staxv-hypervisor
Sibling product, separate repo. The hypervisor runs on every physical
node; cluster-manager is the one central service (likely 3-replica HA
when that's designed).

Cluster-manager **consumes** the hypervisor's public gRPC API (contract
in `staxv-hypervisor/api/` Go submodule). No reaching into hypervisor
internals. Release cadences are independent.

## Status
Planning / pre-scaffold. No Go code. Coding starts after
staxv-hypervisor reaches ~v0.3 (VM basics + multi-tenancy are
prerequisites).

## Origin of the design doc
The full design — connection architecture, enrollment flow, scope
breakdown, open questions — was written in staxv-hypervisor's memory
at `.claude/memory/cluster_manager.md` while designing the hypervisor's
API surface. That copy stays canonical until cluster-manager
development starts, at which point it migrates here.

## GitHub
Repo: https://github.com/zeshaq/staxv-cluster-manager
Project/Kanban: https://github.com/users/zeshaq/projects/3 (shared with
staxv-hypervisor — one board, multiple views per repo)

## Tech stack (planned)
Same house style as hypervisor:
- Go 1.22+
- chi HTTP router (for cluster-manager's own web UI)
- gRPC client (to hypervisors) with mTLS
- SQLite (modernc.org/sqlite) for fleet state
- gorilla/websocket for browser ↔ cluster-manager UI
- React frontend embedded via embed.FS
- TOML config

## Host OS (planned — match hypervisor)
Ubuntu 24.04 LTS, same as hypervisor. No Redfish-specific OS
requirement; cluster-manager is a Go binary + SQLite + TLS certs.

## Multi-tenancy / identity model
**Authoritative user + UID registry** for the fleet. Users are created
once in cluster-manager; cluster-manager provisions Linux accounts on
hypervisors via the `useradd` gRPC RPC using a specific UID (so the
same user has the same UID everywhere — required for live migration
and shared storage).

Hypervisors don't know about each other. Cluster-manager is the only
thing that knows the fleet-wide picture. See staxv-hypervisor's
[multi_tenancy.md § Fleet-Wide vs Per-Hypervisor Identity](https://github.com/zeshaq/staxv-hypervisor/blob/main/.claude/memory/multi_tenancy.md).
