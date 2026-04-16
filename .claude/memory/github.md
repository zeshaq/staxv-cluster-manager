# GitHub & Project Tracking

Repo: https://github.com/zeshaq/staxv-cluster-manager

## Shared project board
Project: https://github.com/users/zeshaq/projects/3 — **shared with
staxv-hypervisor**. One board, multiple views (per-repo, cross-cutting,
in-progress). Issues from both repos live here.

## Labels (locally in this repo)

### Meta / flow
- `status:idea` — rough thought, not committed (yellow)
- `status:planned` — committed, needs design/AC (teal)
- `status:ready` — has AC, can be picked up (green)
- `epic` — tracking issue with sub-issues (purple)

### Cross-cutting
- `bug`, `documentation`, `security`, `infra` (shared names with hypervisor for consistency)

### cluster-manager specific
- `fleet` — node registry, enrollment, health
- `scheduler` — VM placement
- `migration` — live migration orchestration
- `redfish` — BMC integration
- `sre` — site reliability manager, alerts, dashboards
- `users` — fleet-level user federation, UID registry
- `hypervisor-api` — work that depends on or drives changes to the hypervisor's gRPC surface

## Milestones
- `v0.1 — MVP` — fleet registry, enrollment, basic node list & VM view
- `Future` — ideas not yet committed to a version

More milestones added as the roadmap firms up.

## Issue templates
`.github/ISSUE_TEMPLATE/` mirrors the hypervisor set: Feature, Idea,
Epic, Bug.

## Alignment with staxv-hypervisor
Issues that touch the hypervisor API live in **staxv-hypervisor** with
the `cluster-manager` label — because that's where the code change
happens. Pure consumption-side work (scheduler, UI, Redfish, fleet
state) lives in **this** repo.

## Discipline
Same rules as staxv-hypervisor:
1. Every non-trivial issue body cites the memory file it depends on
2. Every cross-cutting memory file ends with `## Related issues`
3. Memory-decision changes → comment on affected issues same session
4. Idea flow: rough thought → `gh issue create` with Idea template → add milestone → promote to planned → add AC → ready → on the board
