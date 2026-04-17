import { Server, Plus, Key, Terminal } from 'lucide-react'

// Placeholder until the fleet backend lands. The structure hints at
// what's coming: enroll flow (one-time token → CSR → cert), node
// list with health, per-node drill-in.
export default function Fleet() {
  return (
    <div className="max-w-7xl mx-auto space-y-6">
      <div className="flex items-end justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-white text-2xl font-bold">Fleet</h1>
          <p className="text-slate-400 text-sm mt-1">Enrolled staxv-hypervisor nodes.</p>
        </div>
        <button
          disabled
          title="Available after fleet backend lands"
          className="flex items-center gap-2 bg-brand-500/40 text-canvas-900 px-4 py-2.5 rounded-lg text-sm font-semibold opacity-60 cursor-not-allowed shadow-lg shadow-brand-500/10"
        >
          <Plus size={14} /> Enroll hypervisor
        </button>
      </div>

      <div className="bg-canvas-800 border border-canvas-500 rounded-xl p-8 text-center">
        <div className="w-14 h-14 rounded-xl bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center mx-auto mb-4">
          <Server size={24} className="text-brand-400" />
        </div>
        <h3 className="text-slate-100 font-semibold text-lg mb-1">No hypervisors enrolled</h3>
        <p className="text-slate-400 text-sm max-w-md mx-auto mb-6">
          The fleet backend (enrollment, gRPC client pool, health tracking) is in progress.
          Once live, enrolled hypervisors will appear here.
        </p>

        <div className="max-w-xl mx-auto text-left bg-canvas-900 border border-canvas-600 rounded-lg p-5">
          <div className="text-slate-400 text-xs font-semibold tracking-wider uppercase mb-3">Planned enrollment flow</div>
          <ol className="space-y-3 text-sm">
            <li className="flex gap-3">
              <span className="flex-shrink-0 w-6 h-6 rounded-full bg-brand-500/20 text-brand-300 text-xs font-bold flex items-center justify-center mt-0.5">1</span>
              <div className="text-slate-300">
                Admin generates a one-time enrollment token on this CM:
                <div className="mt-1 text-[11px] font-mono text-brand-300 bg-canvas-800 rounded px-2 py-1 inline-block">
                  staxv-cluster-manager enroll-token --valid 10m
                </div>
              </div>
            </li>
            <li className="flex gap-3">
              <span className="flex-shrink-0 w-6 h-6 rounded-full bg-brand-500/20 text-brand-300 text-xs font-bold flex items-center justify-center mt-0.5">2</span>
              <div className="text-slate-300">
                On the hypervisor host:
                <div className="mt-1 text-[11px] font-mono text-brand-300 bg-canvas-800 rounded px-2 py-1 inline-block">
                  staxv-hypervisor enroll --manager https://cm:5443 --token &lt;token&gt;
                </div>
              </div>
            </li>
            <li className="flex gap-3">
              <span className="flex-shrink-0 w-6 h-6 rounded-full bg-brand-500/20 text-brand-300 text-xs font-bold flex items-center justify-center mt-0.5">3</span>
              <div className="text-slate-300">
                CSR exchange happens over the token-authenticated HTTPS call.
                Hypervisor receives a signed cert, cluster-manager gets a long-lived mTLS identity.
              </div>
            </li>
          </ol>
          <div className="flex items-center gap-4 mt-4 text-[11px] text-slate-500">
            <span className="flex items-center gap-1.5"><Key size={11} /> mTLS certs</span>
            <span className="flex items-center gap-1.5"><Terminal size={11} /> gRPC on :5443</span>
          </div>
        </div>
      </div>
    </div>
  )
}
