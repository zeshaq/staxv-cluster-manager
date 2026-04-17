import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  HardDrive, Plus, Trash2, RefreshCw, X, CheckCircle, XCircle, AlertTriangle, HelpCircle,
  Eye, EyeOff, ChevronRight,
} from 'lucide-react'
import api from '../api'

// ──────────────────────────────────────────────────────────────
// Status → icon + color mapping. Kept as a function so the
// server cards and the empty-state placeholder can share it.
// ──────────────────────────────────────────────────────────────
function StatusBadge({ status }) {
  const variants = {
    reachable: {
      icon: CheckCircle, text: 'text-lime-300', bg: 'bg-lime-500/10',
      ring: 'ring-lime-500/30', label: 'Reachable',
    },
    unreachable: {
      icon: XCircle, text: 'text-red-300', bg: 'bg-red-500/10',
      ring: 'ring-red-500/30', label: 'Unreachable',
    },
    error: {
      icon: AlertTriangle, text: 'text-amber-300', bg: 'bg-amber-500/10',
      ring: 'ring-amber-500/30', label: 'Error',
    },
    unknown: {
      icon: HelpCircle, text: 'text-slate-400', bg: 'bg-slate-500/10',
      ring: 'ring-slate-500/30', label: 'Unknown',
    },
  }
  const v = variants[status] || variants.unknown
  const Icon = v.icon
  return (
    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full ${v.bg} ${v.text} ring-1 ${v.ring} text-[11px] font-medium`}>
      <Icon size={11} /> {v.label}
    </span>
  )
}

// ──────────────────────────────────────────────────────────────
// Enroll form — toggleable panel at the top. Inline instead of
// modal; less code, still out of the way when closed.
// ──────────────────────────────────────────────────────────────
function EnrollPanel({ open, onClose, onEnrolled }) {
  const [form, setForm] = useState({
    name: '', bmc_host: '', bmc_port: '443', bmc_username: '', bmc_password: '',
  })
  const [showPass, setShowPass] = useState(false)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(false)

  if (!open) return null

  const set = (k, v) => setForm(p => ({ ...p, [k]: v }))

  const submit = async e => {
    e.preventDefault()
    setErr('')
    setLoading(true)
    try {
      const body = { ...form, bmc_port: parseInt(form.bmc_port, 10) || 443 }
      await api.post('/servers', body)
      setForm({ name: '', bmc_host: '', bmc_port: '443', bmc_username: '', bmc_password: '' })
      onEnrolled()
      onClose()
    } catch (e) {
      setErr(e.response?.data?.error || 'Enrollment failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="bg-canvas-800 border border-brand-500/40 rounded-xl p-5 ring-1 ring-brand-500/20">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h3 className="text-slate-100 font-semibold">Enroll server</h3>
          <p className="text-slate-400 text-xs mt-0.5">
            We'll test the BMC immediately and mark the server reachable / unreachable.
          </p>
        </div>
        <button onClick={onClose} className="p-1.5 rounded text-slate-500 hover:text-slate-300 hover:bg-canvas-700">
          <X size={14} />
        </button>
      </div>

      {err && (
        <div className="bg-red-900/40 border border-red-700/50 text-red-300 text-sm rounded-md px-3 py-2 mb-4">
          {err}
        </div>
      )}

      <form onSubmit={submit} className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Field label="Name" value={form.name} onChange={v => set('name', v)}
               placeholder="dl385-a" required />
        <Field label="BMC host" value={form.bmc_host} onChange={v => set('bmc_host', v)}
               placeholder="10.0.0.50  or  ilo-dl385-a.local" required />
        <Field label="BMC port" value={form.bmc_port} onChange={v => set('bmc_port', v)}
               placeholder="443" />
        <Field label="BMC username" value={form.bmc_username} onChange={v => set('bmc_username', v)}
               placeholder="Administrator / root" autoComplete="username" required />
        <div className="md:col-span-2">
          <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-400 mb-1.5">
            BMC password
          </label>
          <div className="relative">
            <input
              type={showPass ? 'text' : 'password'}
              value={form.bmc_password}
              onChange={e => set('bmc_password', e.target.value)}
              required
              autoComplete="new-password"
              className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-3 py-2 pr-10 text-sm font-mono focus:outline-none"
            />
            <button type="button" onClick={() => setShowPass(!showPass)}
                    tabIndex={-1}
                    className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300">
              {showPass ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
          </div>
          <p className="text-[11px] text-slate-500 mt-1.5">
            Stored AES-256-GCM encrypted at rest. Never logged or returned over the API.
          </p>
        </div>

        <div className="md:col-span-2 flex items-center justify-end gap-2 pt-2">
          <button type="button" onClick={onClose}
                  className="px-4 py-2 rounded text-slate-400 hover:text-slate-200 text-sm">
            Cancel
          </button>
          <button type="submit" disabled={loading}
                  className="flex items-center gap-2 bg-brand-500 hover:bg-brand-400 disabled:opacity-50 text-canvas-900 font-semibold px-4 py-2 rounded text-sm transition-colors">
            <Plus size={13} /> {loading ? 'Enrolling…' : 'Enroll & test'}
          </button>
        </div>
      </form>
    </div>
  )
}

function Field({ label, value, onChange, ...rest }) {
  return (
    <div>
      <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-400 mb-1.5">{label}</label>
      <input
        type="text"
        value={value}
        onChange={e => onChange(e.target.value)}
        className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-3 py-2 text-sm focus:outline-none"
        {...rest}
      />
    </div>
  )
}

// ──────────────────────────────────────────────────────────────
// One card per server. Action buttons: test, delete.
// ──────────────────────────────────────────────────────────────
function ServerCard({ server, onChanged }) {
  const [busy, setBusy] = useState(null) // 'test' | 'delete'

  const test = async () => {
    setBusy('test')
    try {
      await api.post(`/servers/${server.id}/test`)
      onChanged()
    } catch (e) {
      alert(e.response?.data?.error || 'Test failed')
    } finally {
      setBusy(null)
    }
  }

  const del = async () => {
    if (!confirm(`Delete "${server.name}"? Credentials are erased; the physical server is unaffected.`)) return
    setBusy('delete')
    try {
      await api.delete(`/servers/${server.id}`)
      onChanged()
    } catch (e) {
      alert(e.response?.data?.error || 'Delete failed')
    } finally {
      setBusy(null)
    }
  }

  const last = server.last_seen_at ? new Date(server.last_seen_at).toLocaleString() : '—'

  return (
    <div className="bg-canvas-800 border border-canvas-500 rounded-xl p-5 hover:border-brand-500/40 transition-colors group">
      {/* Identity row — wrapped in Link so click-anywhere-on-name jumps to detail page */}
      <div className="flex items-start justify-between mb-3 gap-3">
        <Link to={`/servers/${server.id}`} className="flex items-start gap-3 min-w-0 flex-1 hover:opacity-90">
          <div className="w-10 h-10 rounded-lg bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center flex-shrink-0">
            <HardDrive size={18} className="text-brand-400" />
          </div>
          <div className="min-w-0">
            <div className="text-slate-100 font-semibold truncate group-hover:text-brand-300 transition-colors flex items-center gap-1">
              {server.name}
              <ChevronRight size={14} className="text-slate-600 group-hover:text-brand-400 transition-colors" />
            </div>
            <div className="text-slate-500 text-xs font-mono truncate">
              {server.bmc_host}{server.bmc_port !== 443 && `:${server.bmc_port}`}
            </div>
          </div>
        </Link>
        <StatusBadge status={server.status} />
      </div>

      {/* Hardware info — populated after a successful probe */}
      {(server.manufacturer || server.model || server.serial) && (
        <div className="border-t border-canvas-500 pt-3 mt-3 space-y-1.5">
          {server.manufacturer && (
            <Row label="Manufacturer" value={server.manufacturer} />
          )}
          {server.model && (
            <Row label="Model" value={server.model} />
          )}
          {server.serial && (
            <Row label="Serial" value={server.serial} mono />
          )}
        </div>
      )}

      {server.status_error && (
        <div className="mt-3 text-[11px] text-red-300/80 bg-red-900/20 border border-red-900/40 rounded px-2 py-1.5 font-mono break-all">
          {server.status_error}
        </div>
      )}

      <div className="flex items-center justify-between mt-4 pt-3 border-t border-canvas-500">
        <span className="text-[10px] text-slate-600 tracking-wider uppercase">Last seen {last}</span>
        <div className="flex items-center gap-1.5">
          <button onClick={test} disabled={busy !== null}
                  title="Re-test reachability"
                  className="flex items-center gap-1 px-2.5 py-1 rounded text-slate-400 hover:text-brand-300 hover:bg-canvas-700 disabled:opacity-50 text-xs font-medium transition-colors">
            <RefreshCw size={12} className={busy === 'test' ? 'animate-spin' : ''} />
            Test
          </button>
          <button onClick={del} disabled={busy !== null}
                  title="Delete"
                  className="p-1.5 rounded text-slate-500 hover:text-red-400 hover:bg-canvas-700 disabled:opacity-50">
            <Trash2 size={12} />
          </button>
        </div>
      </div>
    </div>
  )
}

function Row({ label, value, mono }) {
  return (
    <div className="flex justify-between items-baseline gap-3 text-xs">
      <span className="text-slate-500">{label}</span>
      <span className={`text-slate-200 truncate text-right ${mono ? 'font-mono' : ''}`} title={value}>
        {value}
      </span>
    </div>
  )
}

// ──────────────────────────────────────────────────────────────
// Page
// ──────────────────────────────────────────────────────────────
export default function Servers() {
  const [servers, setServers] = useState([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [enrolling, setEnrolling] = useState(false)

  const fetchAll = () => {
    setLoading(true)
    api.get('/servers')
      .then(r => setServers(r.data.servers || []))
      .catch(e => setErr(e.response?.data?.error || 'Failed to load servers'))
      .finally(() => setLoading(false))
  }
  useEffect(fetchAll, [])

  return (
    <div className="max-w-7xl mx-auto space-y-5">
      <div className="flex items-end justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-white text-2xl font-bold">Physical Servers</h1>
          <p className="text-slate-400 text-sm mt-1">
            Bare-metal inventory managed over Redfish (iLO / iDRAC).
          </p>
        </div>
        {!enrolling && (
          <button onClick={() => setEnrolling(true)}
                  className="flex items-center gap-2 bg-brand-500 hover:bg-brand-400 text-canvas-900 px-4 py-2.5 rounded-lg text-sm font-semibold transition-colors shadow-lg shadow-brand-500/20">
            <Plus size={14} /> Enroll server
          </button>
        )}
      </div>

      <EnrollPanel
        open={enrolling}
        onClose={() => setEnrolling(false)}
        onEnrolled={fetchAll}
      />

      {err && (
        <div className="bg-red-900/40 border border-red-700/50 text-red-300 text-sm rounded-md px-4 py-3">
          {err}
        </div>
      )}

      {loading ? (
        <div className="text-brand-400 text-center py-12">Loading…</div>
      ) : servers.length === 0 && !enrolling ? (
        <div className="bg-canvas-800 border border-dashed border-canvas-500 rounded-xl p-10 text-center">
          <div className="w-14 h-14 rounded-xl bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center mx-auto mb-4">
            <HardDrive size={24} className="text-brand-400" />
          </div>
          <h3 className="text-slate-100 font-semibold text-lg mb-1">No servers enrolled</h3>
          <p className="text-slate-400 text-sm max-w-md mx-auto mb-5">
            Enroll a server by its BMC IP / hostname and credentials.
            We'll test reachability over Redfish and fetch model + serial automatically.
          </p>
          <button onClick={() => setEnrolling(true)}
                  className="inline-flex items-center gap-2 bg-brand-500 hover:bg-brand-400 text-canvas-900 font-semibold px-4 py-2.5 rounded text-sm transition-colors">
            <Plus size={14} /> Enroll first server
          </button>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {servers.map(s => (
            <ServerCard key={s.id} server={s} onChanged={fetchAll} />
          ))}
        </div>
      )}
    </div>
  )
}
