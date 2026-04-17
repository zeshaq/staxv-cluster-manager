import { useEffect, useState } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import {
  ChevronLeft, HardDrive, RefreshCw, Trash2, CheckCircle, XCircle,
  AlertTriangle, HelpCircle, Cpu, MemoryStick, Tag, Network,
  Power, Thermometer, Key,
} from 'lucide-react'
import api from '../api'

// Status pill shared with the list page. Kept as a local helper so the
// detail page stays self-contained — duplication is a few lines and
// lets each page evolve independently.
function StatusBadge({ status, big = false }) {
  const v = {
    reachable:   { icon: CheckCircle,   color: 'lime',   label: 'Reachable' },
    unreachable: { icon: XCircle,       color: 'red',    label: 'Unreachable' },
    error:       { icon: AlertTriangle, color: 'amber',  label: 'Error' },
    unknown:     { icon: HelpCircle,    color: 'slate',  label: 'Unknown' },
  }[status] || { icon: HelpCircle, color: 'slate', label: status || 'Unknown' }
  const Icon = v.icon
  const cls = big ? 'px-3 py-1 text-sm' : 'px-2 py-0.5 text-[11px]'
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full bg-${v.color}-500/10 text-${v.color}-300 ring-1 ring-${v.color}-500/30 font-medium ${cls}`}>
      <Icon size={big ? 13 : 11} /> {v.label}
    </span>
  )
}

// Power-state pill. BMCs report the literal Redfish PowerState string;
// we colorize the common ones and show the raw text for anything else.
function PowerBadge({ state }) {
  if (!state) return <span className="text-slate-500 text-xs">—</span>
  const colors = {
    On:          'bg-lime-500/10 text-lime-300 ring-lime-500/30',
    Off:         'bg-slate-500/10 text-slate-300 ring-slate-500/30',
    PoweringOn:  'bg-amber-500/10 text-amber-300 ring-amber-500/30',
    PoweringOff: 'bg-amber-500/10 text-amber-300 ring-amber-500/30',
  }[state] || 'bg-brand-500/10 text-brand-300 ring-brand-500/30'
  return (
    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full ${colors} ring-1 text-[11px] font-medium`}>
      <Power size={11} /> {state}
    </span>
  )
}

// Health pill — Redfish Status.Health: OK / Warning / Critical.
function HealthBadge({ health }) {
  if (!health) return <span className="text-slate-500 text-xs">—</span>
  const colors = {
    OK:       'bg-lime-500/10 text-lime-300 ring-lime-500/30',
    Warning:  'bg-amber-500/10 text-amber-300 ring-amber-500/30',
    Critical: 'bg-red-500/10 text-red-300 ring-red-500/30',
  }[health] || 'bg-slate-500/10 text-slate-300 ring-slate-500/30'
  return (
    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full ${colors} ring-1 text-[11px] font-medium`}>
      <Thermometer size={11} /> {health}
    </span>
  )
}

// One row in a "Spec sheet" section. value defaults to em-dash so
// every row renders even when the BMC didn't populate the field.
function Row({ label, value, mono = false, className = '' }) {
  const v = value ?? ''
  return (
    <div className={`flex items-baseline justify-between gap-4 py-2 border-b border-canvas-500 last:border-0 ${className}`}>
      <span className="text-slate-500 text-xs font-medium uppercase tracking-wider flex-shrink-0">{label}</span>
      <span className={`text-slate-200 text-sm truncate ${mono ? 'font-mono' : ''}`} title={String(v)}>
        {v === '' || v === null || v === undefined ? <span className="text-slate-600">—</span> : v}
      </span>
    </div>
  )
}

function Section({ icon: Icon, title, children }) {
  return (
    <div className="bg-canvas-800 border border-canvas-500 rounded-xl overflow-hidden">
      <div className="px-5 py-3 border-b border-canvas-500 flex items-center gap-2">
        {Icon && <Icon size={13} className="text-brand-400" />}
        <h3 className="text-slate-300 text-xs font-semibold tracking-wider uppercase">{title}</h3>
      </div>
      <div className="px-5 py-1">{children}</div>
    </div>
  )
}

export default function ServerDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [server, setServer] = useState(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(null)  // 'test' | 'delete'

  const fetchOne = async () => {
    setLoading(true)
    try {
      const r = await api.get(`/servers/${id}`)
      setServer(r.data)
      setErr('')
    } catch (e) {
      setErr(e.response?.status === 404 ? 'Server not found' : (e.response?.data?.error || 'Load failed'))
      setServer(null)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { fetchOne() }, [id])  // eslint-disable-line

  const test = async () => {
    setBusy('test')
    try {
      const r = await api.post(`/servers/${id}/test`)
      setServer(r.data)
    } catch (e) {
      alert(e.response?.data?.error || 'Test failed')
    } finally {
      setBusy(null)
    }
  }

  const del = async () => {
    if (!confirm(`Delete "${server.name}"? The physical server is unaffected; credentials are erased.`)) return
    setBusy('delete')
    try {
      await api.delete(`/servers/${id}`)
      navigate('/servers', { replace: true })
    } catch (e) {
      alert(e.response?.data?.error || 'Delete failed')
      setBusy(null)
    }
  }

  if (loading && !server) {
    return <div className="text-brand-400 text-center py-16">Loading…</div>
  }
  if (err) {
    return (
      <div className="max-w-3xl mx-auto">
        <Link to="/servers" className="inline-flex items-center gap-2 text-slate-400 hover:text-brand-300 text-sm mb-6">
          <ChevronLeft size={14} /> Back to servers
        </Link>
        <div className="bg-red-900/30 border border-red-700/50 text-red-300 rounded-xl px-5 py-4">{err}</div>
      </div>
    )
  }
  if (!server) return null

  const last = server.last_seen_at ? new Date(server.last_seen_at).toLocaleString() : '—'
  const created = new Date(server.created_at).toLocaleString()
  const updated = new Date(server.updated_at).toLocaleString()

  return (
    <div className="max-w-5xl mx-auto space-y-5">
      {/* Back link */}
      <Link to="/servers" className="inline-flex items-center gap-2 text-slate-400 hover:text-brand-300 text-sm">
        <ChevronLeft size={14} /> Servers
      </Link>

      {/* Header card */}
      <div className="bg-canvas-800 border border-canvas-500 rounded-xl p-6 relative overflow-hidden">
        <div className="absolute -top-20 -right-20 w-64 h-64 bg-brand-500/10 rounded-full blur-3xl pointer-events-none hero-glow" />
        <div className="relative flex items-start justify-between gap-4 flex-wrap">
          <div className="flex items-start gap-4 min-w-0">
            <div className="w-12 h-12 rounded-xl bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center flex-shrink-0">
              <HardDrive size={22} className="text-brand-400" />
            </div>
            <div className="min-w-0">
              <h1 className="text-white text-2xl font-bold truncate">{server.name}</h1>
              <div className="flex items-center gap-2 mt-1 flex-wrap">
                <span className="text-slate-400 text-sm font-mono">
                  {server.bmc_host}{server.bmc_port !== 443 && `:${server.bmc_port}`}
                </span>
                {server.model && <span className="text-slate-600">·</span>}
                {server.model && <span className="text-slate-400 text-sm">{server.model}</span>}
              </div>
              <div className="flex items-center gap-2 mt-3">
                <StatusBadge status={server.status} big />
                <PowerBadge state={server.power_state} />
                <HealthBadge health={server.health} />
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <button
              onClick={test}
              disabled={busy !== null}
              className="flex items-center gap-2 bg-canvas-700 hover:bg-canvas-600 text-slate-300 px-3.5 py-2 rounded-lg text-sm font-medium border border-canvas-500 disabled:opacity-50 transition-colors"
            >
              <RefreshCw size={13} className={busy === 'test' ? 'animate-spin' : ''} />
              {busy === 'test' ? 'Testing…' : 'Refresh'}
            </button>
            <button
              onClick={del}
              disabled={busy !== null}
              className="flex items-center gap-2 bg-red-900/30 hover:bg-red-900/50 text-red-300 px-3.5 py-2 rounded-lg text-sm font-medium border border-red-700/40 disabled:opacity-50 transition-colors"
            >
              <Trash2 size={13} /> Delete
            </button>
          </div>
        </div>

        {server.status_error && (
          <div className="relative mt-5 bg-red-900/20 border border-red-900/40 rounded-lg p-3">
            <div className="text-[11px] font-semibold tracking-wider uppercase text-red-400 mb-1">Last error</div>
            <div className="text-red-200/90 text-xs font-mono break-all">{server.status_error}</div>
          </div>
        )}
      </div>

      {/* Detail grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
        <Section icon={Tag} title="Identification">
          <Row label="Manufacturer" value={server.manufacturer} />
          <Row label="Model" value={server.model} />
          <Row label="Serial" value={server.serial} mono />
          <Row label="BIOS version" value={server.bios_version} mono />
          <Row label="Hostname" value={server.hostname} mono />
        </Section>

        <Section icon={Cpu} title="Hardware">
          <Row label="CPU count" value={server.cpu_count || null} />
          <Row label="Memory" value={server.memory_gb ? `${server.memory_gb} GB` : null} />
          <Row
            label="Power state"
            value={server.power_state ? <PowerBadge state={server.power_state} /> : null}
          />
          <Row
            label="Health"
            value={server.health ? <HealthBadge health={server.health} /> : null}
          />
        </Section>

        <Section icon={Key} title="BMC">
          <Row label="Host" value={server.bmc_host} mono />
          <Row label="Port" value={server.bmc_port} />
          <Row label="Username" value={server.bmc_username} mono />
          <Row
            label="Password"
            value={
              <span className="inline-flex items-center gap-1.5 text-slate-400 text-xs">
                <span className="font-mono">••••••••</span>
                <span className="text-slate-600">encrypted at rest</span>
              </span>
            }
          />
        </Section>

        <Section icon={Network} title="Metadata">
          <Row label="Last seen" value={last} />
          <Row label="Enrolled" value={created} />
          <Row label="Updated" value={updated} />
        </Section>
      </div>

      {/* Deferred actions — flagged so the user knows more is coming */}
      <div className="bg-canvas-800 border border-dashed border-canvas-500 rounded-xl p-5">
        <div className="flex items-start gap-3">
          <div className="w-8 h-8 rounded-lg bg-canvas-700 flex items-center justify-center flex-shrink-0 mt-0.5">
            <Power size={14} className="text-slate-500" />
          </div>
          <div className="flex-1">
            <h4 className="text-slate-300 text-sm font-medium">Power actions coming next</h4>
            <p className="text-slate-500 text-xs mt-1">
              Power On / Graceful Shutdown / Force Off / Reset — via Redfish <code className="text-slate-400 bg-canvas-700 px-1 rounded">ComputerSystem.Reset</code>. Hardware inventory drill-down (CPUs, DIMMs, disks, NICs) and thermal sensors after that.
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}
