import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Server, Cpu, MemoryStick, HardDrive, Activity, Clock,
  Plus, ArrowRight, Network,
} from 'lucide-react'
import api from '../api'

function StatCard({ icon: Icon, label, value, sub, accent = 'brand', progress }) {
  const c = {
    brand: { text: 'text-brand-300',  bg: 'bg-brand-500/10',  ring: 'ring-brand-500/20',  bar: 'bg-brand-500' },
    lime:  { text: 'text-lime-300',   bg: 'bg-lime-500/10',   ring: 'ring-lime-500/20',   bar: 'bg-lime-400'  },
    amber: { text: 'text-amber-300',  bg: 'bg-amber-500/10',  ring: 'ring-amber-500/20',  bar: 'bg-amber-400' },
    slate: { text: 'text-slate-300',  bg: 'bg-slate-500/10',  ring: 'ring-slate-500/20',  bar: 'bg-slate-400' },
  }[accent] || {}
  const barColor = progress > 85 ? 'bg-red-500' : progress > 65 ? 'bg-amber-400' : c.bar

  return (
    <div className={`bg-canvas-800 border border-canvas-500 rounded-xl p-5 ring-1 ${c.ring}`}>
      <div className="flex items-center justify-between mb-3">
        <span className="text-slate-400 text-xs font-semibold tracking-wider uppercase">{label}</span>
        {Icon && <div className={`p-1.5 rounded-lg ${c.bg}`}><Icon size={14} className={c.text} /></div>}
      </div>
      <div className={`text-2xl font-bold ${c.text}`}>{value ?? '—'}</div>
      {sub && <div className="text-slate-500 text-xs mt-1">{sub}</div>}
      {progress !== undefined && (
        <div className="mt-3">
          <div className="bg-canvas-700 rounded-full h-1.5 overflow-hidden">
            <div className={`${barColor} h-1.5 rounded-full transition-all`} style={{ width: `${Math.min(progress, 100)}%` }} />
          </div>
        </div>
      )}
    </div>
  )
}

function SectionHeader({ title, sub, linkTo, linkLabel }) {
  return (
    <div className="flex items-end justify-between mb-4">
      <div>
        <h3 className="text-slate-100 font-semibold text-sm">{title}</h3>
        {sub && <p className="text-slate-500 text-xs mt-0.5">{sub}</p>}
      </div>
      {linkTo && (
        <Link to={linkTo} className="text-brand-400 hover:text-brand-300 text-xs font-medium flex items-center gap-1">
          {linkLabel} <ArrowRight size={11} />
        </Link>
      )}
    </div>
  )
}

export default function Home() {
  const [dash, setDash]     = useState(null)
  const [err, setErr]       = useState('')
  const [loading, setLoading] = useState(true)

  const fetchAll = () => {
    setLoading(true)
    api.get('/dashboard')
      .then(r => setDash(r.data))
      .catch(e => setErr(e.response?.data?.error || 'Failed to load dashboard'))
      .finally(() => setLoading(false))
  }
  useEffect(fetchAll, [])

  if (loading) return <div className="text-brand-400 text-center py-20">Loading…</div>
  if (err)     return <div className="text-red-400 text-center py-20">{err}</div>

  const { uptime_str, cpu_percent, load_avg, mem, disk, net } = dash || {}

  // Fleet stats are placeholders until the fleet backend lands.
  // Shown as zeros so the admin knows these are real counters
  // awaiting wiring, not broken data.
  const fleet = { nodes: 0, vms: 0, clusters: 0 }

  return (
    <div className="space-y-6 max-w-7xl mx-auto">
      {/* ───────────── Hero ───────────── */}
      <div className="relative bg-canvas-800 border border-canvas-500 rounded-2xl p-6 overflow-hidden">
        <div className="absolute -top-20 -right-20 w-64 h-64 bg-brand-500/10 rounded-full blur-3xl pointer-events-none hero-glow" />
        <div className="relative flex items-start justify-between gap-6 flex-wrap">
          <div className="flex items-start gap-4">
            <img src="/logo.svg" alt="StaxV" className="w-14 h-14 mt-1" />
            <div>
              <h1 className="text-white text-2xl font-bold leading-tight">Cluster Overview</h1>
              <p className="text-slate-400 text-sm mt-1">
                Control plane for your fleet of <span className="text-brand-300">staxv-hypervisor</span> nodes.
              </p>
            </div>
          </div>
          <Link
            to="/fleet"
            className="flex items-center gap-2 bg-brand-500 hover:bg-brand-400 text-canvas-900 px-4 py-2.5 rounded-lg text-sm font-semibold transition-colors shadow-lg shadow-brand-500/20"
          >
            <Plus size={14} /> Enroll hypervisor
          </Link>
        </div>
      </div>

      {/* ───────────── Infrastructure snapshot ───────────── */}
      <div>
        <SectionHeader title="Infrastructure" sub="Physical inventory and logical hypervisor fleet." />
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <StatCard icon={HardDrive} label="Physical Servers" value={0}              sub="Via iLO / iDRAC"        accent="brand" />
          <StatCard icon={Server}    label="Hypervisors"      value={fleet.nodes}    sub="Enrolled nodes"         accent="brand" />
          <StatCard icon={Cpu}       label="VMs"              value={fleet.vms}      sub="Across all hypervisors" accent="lime" />
          <StatCard icon={Network}   label="Clusters"         value={fleet.clusters} sub="Migration-capable groups" accent="slate" />
        </div>
        {fleet.nodes === 0 && (
          <div className="mt-4 bg-canvas-800 border border-dashed border-canvas-500 rounded-xl p-5 flex items-center justify-between flex-wrap gap-3">
            <div>
              <h4 className="text-slate-200 font-medium text-sm mb-0.5">No hypervisors enrolled</h4>
              <p className="text-slate-500 text-xs">
                Enroll your first <span className="text-brand-300 font-mono">staxv-hypervisor</span> node to start managing VMs fleet-wide.
              </p>
            </div>
            <Link to="/fleet" className="text-brand-400 hover:text-brand-300 text-sm font-medium flex items-center gap-1">
              Get started <ArrowRight size={13} />
            </Link>
          </div>
        )}
      </div>

      {/* ───────────── Control-plane host health ───────────── */}
      <div>
        <SectionHeader
          title="Control plane"
          sub="Metrics for this staxv-cluster-manager host (not fleet-wide)."
        />
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <StatCard icon={Clock}        label="Uptime" value={uptime_str} accent="slate" />
          <StatCard icon={Cpu}          label="CPU"    value={`${cpu_percent}%`}
            sub={`Load: ${load_avg?.[0]} / ${load_avg?.[1]} / ${load_avg?.[2]}`}
            progress={cpu_percent} accent="brand" />
          <StatCard icon={MemoryStick}  label="Memory" value={`${mem?.percent}%`}
            sub={`${mem?.used_gb} / ${mem?.total_gb} GB`}
            progress={mem?.percent} accent="lime" />
          <StatCard icon={HardDrive}    label="Disk (/)" value={`${disk?.percent}%`}
            sub={`${disk?.used_gb} / ${disk?.total_gb} GB`}
            progress={disk?.percent} accent="amber" />
        </div>

        <div className="mt-4 grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="bg-canvas-800 border border-canvas-500 rounded-xl p-5">
            <div className="text-slate-400 text-xs font-semibold tracking-wider uppercase mb-3">Network I/O</div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <div className="text-slate-500 text-xs mb-1">↓ Received</div>
                <div className="text-slate-200 font-mono text-sm">{bytesHuman(net?.bytes_recv)}</div>
              </div>
              <div>
                <div className="text-slate-500 text-xs mb-1">↑ Sent</div>
                <div className="text-slate-200 font-mono text-sm">{bytesHuman(net?.bytes_sent)}</div>
              </div>
            </div>
          </div>
          <div className="bg-canvas-800 border border-canvas-500 rounded-xl p-5">
            <div className="text-slate-400 text-xs font-semibold tracking-wider uppercase mb-3">Build</div>
            <div className="text-slate-200 text-sm">
              <span className="text-slate-500">component:</span> staxv-cluster-manager
            </div>
            <div className="text-slate-200 text-sm mt-1">
              <span className="text-slate-500">version:</span> dev
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function bytesHuman(b) {
  if (b == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = b, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(1)} ${u[i]}`
}
