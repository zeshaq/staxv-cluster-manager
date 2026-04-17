import { Users as UsersIcon, UserPlus, Shield } from 'lucide-react'

// Placeholder. Real scope: create CM admin users (already works via the
// useradd CLI and will work via /api/admin/users when wired), manage
// fleet-wide identities that propagate to hypervisors via their
// useradd gRPC RPC. The latter is the headline feature — one user
// creation call, UID coordinated, account materialized on every
// hypervisor where that user is placed.
export default function Users() {
  return (
    <div className="max-w-7xl mx-auto space-y-6">
      <div className="flex items-end justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-white text-2xl font-bold">Users</h1>
          <p className="text-slate-400 text-sm mt-1">
            Fleet-wide identity — one user registry, UID coordinated across all hypervisors.
          </p>
        </div>
        <button
          disabled
          title="Available after users backend lands"
          className="flex items-center gap-2 bg-brand-500/40 text-canvas-900 px-4 py-2.5 rounded-lg text-sm font-semibold opacity-60 cursor-not-allowed"
        >
          <UserPlus size={14} /> New user
        </button>
      </div>

      <div className="bg-canvas-800 border border-canvas-500 rounded-xl p-8 text-center">
        <div className="w-14 h-14 rounded-xl bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center mx-auto mb-4">
          <UsersIcon size={24} className="text-brand-400" />
        </div>
        <h3 className="text-slate-100 font-semibold text-lg mb-1">Directory not yet wired</h3>
        <p className="text-slate-400 text-sm max-w-md mx-auto mb-6">
          Cluster-manager is the authoritative UID registry. When a user is created here,
          the fleet backend will propagate Linux accounts to each hypervisor via the
          useradd gRPC RPC — same UID everywhere, required for live migration and
          shared-storage permissions.
        </p>

        <div className="max-w-xl mx-auto text-left bg-canvas-900 border border-canvas-600 rounded-lg p-5">
          <div className="text-slate-400 text-xs font-semibold tracking-wider uppercase mb-3 flex items-center gap-2">
            <Shield size={12} /> UID coordination
          </div>
          <p className="text-slate-400 text-sm leading-relaxed">
            Same UID on every hypervisor where the user exists — required for live
            migration (qcow2 file ownership) and shared storage (NFS/iSCSI POSIX perms).
            Pre-cluster-manager this is manual discipline. Post-CM, automatic.
          </p>
        </div>
      </div>
    </div>
  )
}
