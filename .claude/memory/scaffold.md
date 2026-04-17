# Scaffold

Cluster-manager is scaffolded as a near-clone of staxv-hypervisor's
v0.1 base. Same patterns, narrower surface, different fleet-scoped
features to come.

## What's here

```
staxv-cluster-manager/
├── go.mod                  — module: github.com/zeshaq/staxv-cluster-manager
├── cmd/staxv-cluster-manager/main.go
│                              subcommands: serve, useradd, migrate, version
├── internal/
│   ├── config/config.go   — TOML loader
│   ├── db/                — SQLite, migrations (0001_users, 0002_settings)
│   ├── handlers/          — auth, settings, host, dashboard
│   └── webui/             — embed.FS stub (empty dist until a frontend lands)
├── pkg/
│   ├── auth/              — User, Signer, Middleware, CredentialVerifier
│   ├── secrets/           — AES-256-GCM AEAD + LoadOrCreateKey
│   ├── pamauth/           — pamtester wrapper
│   └── hostinfo/          — gopsutil-backed dashboard snapshot (of CM host)
├── Makefile / .air.toml / dev-config.toml.example
└── .claude/memory/        — this directory
```

## Runs on port :5002

Chosen to coexist on dl385-2 alongside:
- `:5000` — vm-manager (Python legacy)
- `:5001` — staxv-hypervisor (dev)
- `:5002` — staxv-cluster-manager (dev)
- `:5443` — future gRPC port (hypervisor-side; CM dials into hypervisors there)

## PAM service name

`/etc/pam.d/staxv-cluster-manager` (distinct from hypervisor's file).
Admin can use different PAM stacks per service if the CM runs on the
same host as a hypervisor. Typically:

```
@include common-auth
@include common-account
```

## Code duplication with staxv-hypervisor

`pkg/auth`, `pkg/secrets`, `pkg/pamauth`, `pkg/hostinfo` are **copies**
of the hypervisor's versions. Roughly 1000 lines duplicated.

### Why copy vs share?
- **Not a third repo** — we committed to not creating `staxv-shared`
  until 3+ consumers exist (see hypervisor's `cluster_manager.md`
  §"API Contract — Where It Lives").
- **Not importing from hypervisor repo** — would couple cluster-manager's
  dependency graph to hypervisor's (bcrypt cost bumps, gopsutil
  upgrades, etc. would cascade).
- **Not promoting to hypervisor's api/ submodule** — that submodule is
  the wire contract; stretching it to "wire contract + utility libs"
  muddies the boundary.

### When to consolidate
When:
- Both repos are stable (low code churn in pkg/)
- A third consumer appears (CLI, exporter, Terraform provider)
- The duplicated code's divergence between repos starts hurting

Cleanest path: extract to a fourth module `staxv-hypervisor/shared/`
(multi-module repo, like `api/`) and update both to import from
there. Or a genuinely-separate `staxv-shared` repo if we're past the
"three consumers" threshold by then.

Until then: **keep the duplicate in sync by hand when fixing bugs**.
Document any intentional divergence inline with `// cluster-manager:
diverges from hypervisor because …`.

## What the scaffold does NOT include

Features specific to cluster-manager's job — built after this base is
running:

- **Fleet registry + enrollment** — `internal/fleet/`, hypervisor cert
  exchange (see `project_context.md` for the enrollment flow)
- **gRPC client pool** — `internal/grpcclient/`, uses
  `github.com/zeshaq/staxv-hypervisor/api` once that submodule is scaffolded
- **Redfish client** — `internal/redfish/` (talks direct to BMCs)
- **Scheduler** — VM placement
- **Migration orchestration** — `internal/migration/`
- **SRE** — alerts, SLIs, fleet-wide health aggregation
- **Fleet user federation** — propagate users to hypervisors via their
  `useradd` gRPC RPC
- **Its own React frontend** — separate from hypervisor's, different
  navigation. When built, lives in `frontend/` and `make frontend`
  handles the Vite build + embed.

## Verification

The scaffold should pass the same smoke test as hypervisor v0.1:

```
make dev              # air → builds + serves on :5002
./tmp/staxv-cluster-manager useradd --username admin --admin
  password: ****
curl ... /api/auth/login         → 200 + cookie
curl ... /api/auth/me            → 200 + user JSON
curl ... /api/healthz            → {"status":"ok"}
curl ... /api/dashboard          → CPU/mem/disk/net/procs (of CM host)
```
