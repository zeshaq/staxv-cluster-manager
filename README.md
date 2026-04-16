# staxv-cluster-manager

Fleet control plane for [staxv-hypervisor](https://github.com/zeshaq/staxv-hypervisor) nodes. Think of it as the vCenter to staxv-hypervisor's ESXi: a central service that manages a cluster of hypervisors as a single system.

> **Status**: planning / pre-scaffold. Design doc is in place; coding starts after staxv-hypervisor reaches ~v0.3 (VM basics + multi-tenancy land first).

## Responsibilities

- **Fleet identity** — authoritative user + UID registry across all hypervisors, single sign-on, unified per-user VM view
- **Redfish management** — power, hardware inventory, firmware, sensors via BMCs (iDRAC / iLO) — cluster-manager talks to BMCs directly, not through the hypervisor
- **Live migration orchestration** — tells source and destination hypervisors to negotiate migration; QEMU's migration protocol carries the memory pages hypervisor-to-hypervisor, cluster-manager is not in the data path
- **Scheduler** — picks which hypervisor a new VM lands on based on resources, tenant affinity, policy
- **Site Reliability Manager** — fleet health, alerts, maintenance modes
- **Hypervisor clustering** — group hypervisors that share storage/network so VMs can move between them

## How it connects to hypervisors

Three channels, each with a distinct responsibility:

1. **gRPC + mTLS** — primary control channel. Cluster-manager → each hypervisor on `:5443`. Unary RPCs for commands, server streams for events.
2. **Redfish HTTPS** — cluster-manager → server BMCs directly. Not through the hypervisor.
3. **QEMU migration** — hypervisor → hypervisor directly. Cluster-manager orchestrates but stays out of the bytes.

Enrollment is a one-time cert exchange. No shared secrets end up in any config file.

## Why a separate repo (not a monorepo with hypervisor)

- **Different product shape**: per-node agent vs central HA control plane. Different install, different uptime guarantees, different trust boundaries.
- **Release independence**: must upgrade cluster-manager without touching every hypervisor (vCenter 8 / ESXi 7 model).
- **Clean API boundary**: forces the gRPC contract to be the *only* way in. No reaching into hypervisor internals.
- **Prior art**: oVirt engine + vdsm are separate repos. It's the idiomatic KVM/libvirt split.

## API contract

The contract lives in [staxv-hypervisor's `api/` Go submodule](https://github.com/zeshaq/staxv-hypervisor/tree/main/api) (when it's scaffolded). This repo imports it via `go.mod`. No separate `staxv-api` repo — pattern revisited when 3+ consumers exist.

## Development status

This repo is seeded for planning only — issues, labels, milestones, and memory exist; no Go code yet. See the [project board](https://github.com/users/zeshaq/projects/3) (shared with staxv-hypervisor) and the design doc in [staxv-hypervisor/.claude/memory/cluster_manager.md](https://github.com/zeshaq/staxv-hypervisor/blob/main/.claude/memory/cluster_manager.md).

## License

Apache 2.0 — same as staxv-hypervisor.
