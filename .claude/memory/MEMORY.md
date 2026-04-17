# Memory Index

- [Project context](project_context.md) — what staxv-cluster-manager is, relationship to hypervisor, roadmap
- [Scaffold](scaffold.md) — repo layout, port assignments, PAM service name, pkg/ duplication policy, verification
- [GitHub & project tracking](github.md) — labels, milestones, cross-repo conventions

## Design source of truth

The full design for cluster-manager currently lives in the **staxv-hypervisor** repo at [`.claude/memory/cluster_manager.md`](https://github.com/zeshaq/staxv-hypervisor/blob/main/.claude/memory/cluster_manager.md). It was written while designing the hypervisor's API contract and stayed there to keep momentum.

**Migrate that doc here when active development on cluster-manager begins** (expected after hypervisor v0.3). Until then, treat the hypervisor copy as canonical. Don't duplicate it — update it in one place.

## Sibling repo on disk
- `../staxv-hypervisor/` — the agent that runs on each hypervisor node
- Both repos share one GitHub Project: https://github.com/users/zeshaq/projects/3
