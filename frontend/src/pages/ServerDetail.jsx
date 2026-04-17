import { useEffect, useState } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import {
  ChevronLeft, ChevronDown, ChevronRight,
  HardDrive, RefreshCw, Trash2, CheckCircle, XCircle,
  AlertTriangle, HelpCircle, Cpu, MemoryStick, Tag, Network,
  Power, PowerOff, RotateCw, Zap, Thermometer, Key, Play, Square,
  Fan, Wind, Gauge, CircuitBoard,
  Disc3, Rocket, X,
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

// ──────────────────────────────────────────────────────────────
// Power action button. Renders a chip-style button with an icon,
// handles confirm for destructive actions, shows a spinner while the
// request is in flight.
//
// Enabled/disabled logic is explicit per-power-state so the UI matches
// what the BMC will accept — no point in clicking "Power On" on a box
// already running.
// ──────────────────────────────────────────────────────────────
function PowerButton({ action, label, icon: Icon, variant, currentState, onPerform, busy }) {
  const tone = {
    safe:   'bg-canvas-700 hover:bg-canvas-600 text-slate-200 border-canvas-500 hover:border-brand-500/60',
    brand:  'bg-brand-500 hover:bg-brand-400 text-canvas-900 border-transparent',
    danger: 'bg-red-900/30 hover:bg-red-900/50 text-red-300 border-red-700/40',
  }[variant] || 'bg-canvas-700 text-slate-200 border-canvas-500'

  // enabled-when logic — conservative. Unknown power state → enable
  // everything so admin isn't stuck; let the BMC reject what it can't do.
  const isOn     = currentState === 'On'
  const isOff    = currentState === 'Off'
  const enabled  = {
    on:            !isOn,
    shutdown:      !isOff,
    reboot:        !isOff,
    force_off:     !isOff,
    force_reboot:  !isOff,
  }[action] ?? true

  const disabled = busy || !enabled

  const run = async () => {
    if (variant === 'danger' && !confirm(`${label} — this skips the guest OS. Continue?`)) return
    await onPerform(action)
  }

  return (
    <button
      onClick={run}
      disabled={disabled}
      title={enabled ? label : `Not available when power state is ${currentState || 'unknown'}`}
      className={`flex items-center gap-1.5 px-3 py-1.5 rounded border text-xs font-semibold transition-colors ${tone} disabled:opacity-40 disabled:cursor-not-allowed`}
    >
      {busy === action ? <RefreshCw size={12} className="animate-spin" /> : <Icon size={12} />}
      {label}
    </button>
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

// Expandable variant of Section. Lazy-loads children on first expand
// so a collapsed section has zero cost. Caches content while the page
// is open — clicking collapse/expand doesn't refetch. A manual reload
// button in the header re-triggers load.
//
// `load` is an async function that returns the data or throws; we
// catch into local state and render `render(data, err)` below.
function ExpandableSection({ icon: Icon, title, subtitle, load, render, defaultOpen = false }) {
  const [open, setOpen] = useState(defaultOpen)
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')

  const doLoad = async () => {
    setLoading(true); setErr('')
    try {
      setData(await load())
    } catch (e) {
      setErr(e.response?.data?.error || e.message || 'Load failed')
    } finally {
      setLoading(false)
    }
  }

  const toggle = () => {
    const next = !open
    setOpen(next)
    // Lazy-load on first expand only. `data === null` means "never loaded" —
    // `[]` or `{}` are valid empty responses we keep cached.
    if (next && data === null && !loading) doLoad()
  }

  const Chevron = open ? ChevronDown : ChevronRight

  return (
    <div className="bg-canvas-800 border border-canvas-500 rounded-xl overflow-hidden">
      <button
        onClick={toggle}
        className="w-full px-5 py-3 border-b border-canvas-500 flex items-center gap-2 hover:bg-canvas-700/50 transition-colors text-left"
      >
        <Chevron size={14} className="text-slate-500 flex-shrink-0" />
        {Icon && <Icon size={13} className="text-brand-400 flex-shrink-0" />}
        <h3 className="text-slate-300 text-xs font-semibold tracking-wider uppercase flex-shrink-0">{title}</h3>
        {subtitle && <span className="text-slate-500 text-xs truncate">· {subtitle}</span>}
        {open && (
          <span
            role="button"
            tabIndex={0}
            onClick={(e) => { e.stopPropagation(); doLoad() }}
            onKeyDown={(e) => { if (e.key === 'Enter') { e.stopPropagation(); doLoad() } }}
            className="ml-auto flex items-center gap-1 text-slate-500 hover:text-brand-300 text-[11px] cursor-pointer"
            title="Reload"
          >
            <RefreshCw size={11} className={loading ? 'animate-spin' : ''} />
            {loading ? 'Loading…' : 'Reload'}
          </span>
        )}
      </button>
      {open && (
        <div className="px-5 py-4">
          {loading && !data && (
            <div className="text-slate-500 text-sm py-6 text-center">Loading…</div>
          )}
          {err && (
            <div className="bg-red-900/20 border border-red-900/40 text-red-300 text-xs rounded-lg px-3 py-2 mb-3 font-mono break-all">{err}</div>
          )}
          {data && render(data, { reload: doLoad })}
        </div>
      )}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────
// ISOPicker — modal for choosing an ISO + slot. Used by both
// "Mount ISO" (single action) and "Boot from ISO" (mount + boot
// override + restart). The caller decides which API to hit and
// what warning to show.
//
// Fetches the ISO catalog on open, filters to status=ready, and
// shows a compact list with OS type + version + size + description.
// ─────────────────────────────────────────────────────────────
function ISOPicker({ open, onClose, title, confirmLabel, confirmTone, warning, slots, defaultSlot, onConfirm }) {
  const [isos, setIsos] = useState([])
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')
  const [selectedIso, setSelectedIso] = useState(null)
  const [slot, setSlot] = useState(defaultSlot || '')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!open) return
    setSelectedIso(null); setErr(''); setSlot(defaultSlot || '')
    setLoading(true)
    api.get('/isos')
      .then(r => setIsos((r.data.isos || []).filter(i => i.status === 'ready')))
      .catch(e => setErr(e.response?.data?.error || 'Failed to load ISOs'))
      .finally(() => setLoading(false))
  }, [open, defaultSlot])

  if (!open) return null

  const submit = async () => {
    if (!selectedIso) { setErr('Pick an ISO'); return }
    setBusy(true); setErr('')
    try {
      await onConfirm(selectedIso, slot)
      onClose()
    } catch (e) {
      setErr(e.response?.data?.error || 'Action failed')
    } finally {
      setBusy(false)
    }
  }

  // CD/DVD-capable slots only — the backend auto-picks one when slot
  // is empty, but letting the admin see + override is useful on boxes
  // with multiple ISO-capable slots (rare but possible).
  const cdSlots = (slots || []).filter(s => (s.media_types || []).some(m => ['CD', 'DVD', 'CDROM'].includes(m.toUpperCase())))

  return (
    <div className="fixed inset-0 z-50 bg-black/70 backdrop-blur-sm flex items-center justify-center p-4" onClick={onClose}>
      <div
        className="bg-canvas-800 border border-canvas-500 rounded-xl w-full max-w-2xl max-h-[85vh] flex flex-col shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="px-5 py-3 border-b border-canvas-500 flex items-center justify-between">
          <h3 className="text-slate-100 font-semibold">{title}</h3>
          <button onClick={onClose} className="p-1 rounded text-slate-500 hover:text-slate-200 hover:bg-canvas-700">
            <X size={14} />
          </button>
        </div>

        <div className="px-5 py-4 overflow-y-auto flex-1">
          {warning && (
            <div className="bg-amber-900/20 border border-amber-900/50 text-amber-200 text-xs rounded-lg px-3 py-2 mb-4 flex items-start gap-2">
              <AlertTriangle size={13} className="flex-shrink-0 mt-0.5" />
              <span>{warning}</span>
            </div>
          )}
          {err && (
            <div className="bg-red-900/30 border border-red-900/50 text-red-300 text-xs rounded-lg px-3 py-2 mb-4 font-mono break-all">{err}</div>
          )}

          {loading ? (
            <div className="text-slate-500 text-sm py-6 text-center">Loading catalog…</div>
          ) : isos.length === 0 ? (
            <div className="text-slate-500 text-sm py-6 text-center italic">
              No ready ISOs. Upload or import one from the ISO library first.
            </div>
          ) : (
            <div className="space-y-1.5 mb-4">
              {isos.map(iso => {
                const sel = selectedIso?.id === iso.id
                return (
                  <button
                    key={iso.id}
                    onClick={() => setSelectedIso(iso)}
                    className={`w-full text-left rounded-lg border px-3 py-2.5 transition-colors flex items-center gap-3 ${
                      sel
                        ? 'bg-brand-500/10 border-brand-500/60 ring-1 ring-brand-500/40'
                        : 'bg-canvas-900/40 border-canvas-500 hover:border-brand-500/30 hover:bg-canvas-700/50'
                    }`}
                  >
                    <Disc3 size={18} className={sel ? 'text-brand-400' : 'text-slate-500'} />
                    <div className="flex-1 min-w-0">
                      <div className="text-slate-100 text-sm font-medium truncate">{iso.name}</div>
                      <div className="text-slate-500 text-[11px] font-mono truncate">
                        {iso.filename} · {fmtBytes(iso.size_bytes)}{iso.os_version ? ` · ${iso.os_version}` : ''}
                      </div>
                    </div>
                    <span className="text-[10px] uppercase tracking-wider text-slate-500 flex-shrink-0">{iso.os_type}</span>
                  </button>
                )
              })}
            </div>
          )}

          {cdSlots.length > 1 && (
            <div>
              <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-400 mb-1.5">
                Virtual media slot
              </label>
              <select
                value={slot}
                onChange={(e) => setSlot(e.target.value)}
                className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-3 py-2 text-sm focus:outline-none"
              >
                <option value="">Auto-pick first CD/DVD slot</option>
                {cdSlots.map(s => (
                  <option key={s.id} value={s.id}>{s.name} ({s.id}){s.inserted ? ' — currently mounted' : ''}</option>
                ))}
              </select>
            </div>
          )}
        </div>

        <div className="px-5 py-3 border-t border-canvas-500 flex items-center justify-end gap-2">
          <button onClick={onClose} disabled={busy} className="px-4 py-2 rounded text-slate-400 hover:text-slate-200 text-sm">
            Cancel
          </button>
          <button
            onClick={submit}
            disabled={busy || !selectedIso}
            className={`flex items-center gap-2 disabled:opacity-50 font-semibold px-4 py-2 rounded text-sm transition-colors ${
              confirmTone === 'danger'
                ? 'bg-red-500 hover:bg-red-400 text-white'
                : 'bg-brand-500 hover:bg-brand-400 text-canvas-900'
            }`}
          >
            {busy ? <RefreshCw size={13} className="animate-spin" /> : <Rocket size={13} />}
            {busy ? 'Working…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}

// SubTable: a compact per-subsection table with header + rows. Shared by
// Hardware and Health blocks so they look consistent. `cols` is an
// array of { header, render(row) }.
function SubTable({ icon: Icon, title, count, error, rows, cols, empty = 'None reported' }) {
  return (
    <div className="mb-5 last:mb-0">
      <div className="flex items-baseline gap-2 mb-2">
        {Icon && <Icon size={12} className="text-brand-400" />}
        <h4 className="text-slate-300 text-xs font-semibold tracking-wider uppercase">{title}</h4>
        <span className="text-slate-500 text-xs">{count}</span>
      </div>
      {error && (
        <div className="bg-red-900/20 border border-red-900/40 text-red-300/90 text-[11px] rounded px-2 py-1 mb-2 font-mono break-all">{error}</div>
      )}
      {!rows || rows.length === 0 ? (
        <div className="text-slate-600 text-xs italic py-2">{empty}</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="text-left text-slate-500 border-b border-canvas-500">
                {cols.map((c, i) => (
                  <th key={i} className="font-medium uppercase tracking-wider py-1.5 pr-4 whitespace-nowrap">{c.header}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={i} className="border-b border-canvas-600/50 last:border-0 text-slate-300">
                  {cols.map((c, j) => (
                    <td key={j} className="py-1.5 pr-4 align-top">{c.render(row) ?? <span className="text-slate-600">—</span>}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// Small health pill for per-component Status.Health coloring.
function Pip({ value }) {
  if (!value) return <span className="text-slate-600">—</span>
  const c = {
    OK:       'bg-lime-500/10 text-lime-300 ring-lime-500/30',
    Warning:  'bg-amber-500/10 text-amber-300 ring-amber-500/30',
    Critical: 'bg-red-500/10 text-red-300 ring-red-500/30',
  }[value] || 'bg-slate-500/10 text-slate-400 ring-slate-500/30'
  return (
    <span className={`inline-block px-1.5 py-0.5 rounded ${c} ring-1 text-[10px] font-medium`}>{value}</span>
  )
}

// Format bytes as a short human string (2 decimals). Redfish returns
// exact byte counts; 1.92 TB is more readable than 1920383410176.
function fmtBytes(n) {
  if (!n) return null
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n, u = 0
  while (v >= 1000 && u < units.length - 1) { v /= 1000; u++ }
  return `${v.toFixed(v < 10 && u > 0 ? 2 : v < 100 ? 1 : 0)} ${units[u]}`
}

// MiB → human. DIMMs report capacity in MiB (8192 = 8 GiB).
function fmtMiB(mib) {
  if (!mib) return null
  if (mib >= 1024) return `${(mib / 1024).toFixed(mib % 1024 === 0 ? 0 : 1)} GiB`
  return `${mib} MiB`
}

export default function ServerDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [server, setServer] = useState(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(null)  // 'test' | 'delete'
  // ISO picker state. mode controls the API + messaging:
  //   'mount' → POST /mount-iso, no restart
  //   'boot'  → POST /boot-from-iso, mount + one-shot boot override + reset
  // slots is passed in from the Virtual Media section so the picker can
  // show a slot dropdown when multiple CD/DVD-capable slots exist.
  const [picker, setPicker] = useState({ open: false, mode: 'mount', slots: [] })

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

  // Power action dispatcher. busy=action name while one is in flight
  // so each button can show its own spinner and all buttons disable
  // together (can't click Power On while Reboot is running).
  const doPower = async (action) => {
    setBusy(action)
    try {
      const r = await api.post(`/servers/${id}/power`, { action })
      setServer(r.data)
    } catch (e) {
      alert(e.response?.data?.error || `Power action "${action}" failed`)
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

      {/* Power actions — Redfish ComputerSystem.Reset.
          BMC action is synchronous(-ish): POST returns in ~1s, re-probe
          follows to refresh power_state. UI shows transitional states
          (PoweringOn etc.) honestly. */}
      <Section icon={Power} title="Power actions">
        <div className="flex flex-wrap gap-2 py-3">
          <PowerButton action="on"           label="Power On"       icon={Play}      variant="brand"
                       currentState={server.power_state} busy={busy} onPerform={doPower} />
          <PowerButton action="shutdown"     label="Shutdown"       icon={Square}    variant="safe"
                       currentState={server.power_state} busy={busy} onPerform={doPower} />
          <PowerButton action="reboot"       label="Restart"        icon={RotateCw}  variant="safe"
                       currentState={server.power_state} busy={busy} onPerform={doPower} />
          <PowerButton action="force_off"    label="Force Off"      icon={PowerOff}  variant="danger"
                       currentState={server.power_state} busy={busy} onPerform={doPower} />
          <PowerButton action="force_reboot" label="Force Restart"  icon={Zap}       variant="danger"
                       currentState={server.power_state} busy={busy} onPerform={doPower} />
        </div>
        <p className="text-slate-500 text-[11px] pb-3 -mt-1">
          Graceful actions ask the guest OS via ACPI. Force actions are immediate and can corrupt dirty filesystems — use only when the OS is unresponsive.
        </p>
      </Section>

      {/* Virtual Media — Redfish VirtualMedia.InsertMedia / EjectMedia.
          Lazy-loaded slot listing (typically CD, USB, Floppy on iLO/iDRAC).
          Mount button picks a ready ISO from the library and tells the
          BMC to fetch it from /iso/{id}/{filename}. "Boot from ISO" is
          the one-shot installer flow: mount + one-shot BootSourceOverride
          + reset. */}
      <ExpandableSection
        icon={Disc3}
        title="Virtual media"
        subtitle="Mount ISOs for OS install"
        load={() => api.get(`/servers/${id}/virtual-media`).then(r => r.data)}
        render={(vm, { reload }) => (
          <VirtualMediaBlock
            slots={vm.slots || []}
            onMount={() => setPicker({ open: true, mode: 'mount',  slots: vm.slots || [] })}
            onBoot ={() => setPicker({ open: true, mode: 'boot',   slots: vm.slots || [] })}
            onEject={async (slot) => {
              try {
                await api.post(`/servers/${id}/eject-iso`, { slot })
                reload()
              } catch (e) {
                alert(e.response?.data?.error || 'Eject failed')
              }
            }}
          />
        )}
      />

      {/* Hardware inventory — CPUs, DIMMs, Drives, NICs.
          Lazy-loaded: first expand fires a single GET that fans out to
          four Redfish collections in parallel on the backend. 2-5s on
          a warm BMC, 5-10s cold. */}
      <ExpandableSection
        icon={CircuitBoard}
        title="Hardware inventory"
        subtitle="Processors, memory, drives, NICs"
        load={() => api.get(`/servers/${id}/hardware`).then(r => r.data)}
        render={(hw) => (
          <>
            <SubTable
              icon={Cpu}
              title="Processors"
              count={hw.processors?.length || 0}
              error={hw.processors_error}
              rows={hw.processors}
              cols={[
                { header: 'Socket', render: r => <span className="font-mono text-slate-400">{r.id || r.name}</span> },
                { header: 'Model', render: r => r.model || r.name },
                { header: 'Cores / Threads', render: r => r.total_cores ? `${r.total_cores} / ${r.total_threads || '—'}` : null },
                { header: 'Max speed', render: r => r.max_speed_mhz ? `${(r.max_speed_mhz/1000).toFixed(1)} GHz` : null },
                { header: 'ISA', render: r => r.instruction_set },
                { header: 'Health', render: r => <Pip value={r.health} /> },
              ]}
            />
            <SubTable
              icon={MemoryStick}
              title="Memory"
              count={(hw.memory?.filter(d => d.capacity_mib > 0).length) || 0}
              error={hw.memory_error}
              rows={hw.memory?.filter(d => d.capacity_mib > 0)}
              cols={[
                { header: 'Slot', render: r => <span className="font-mono text-slate-400">{r.name || r.id}</span> },
                { header: 'Capacity', render: r => fmtMiB(r.capacity_mib) },
                { header: 'Type', render: r => r.memory_device_type },
                { header: 'Speed', render: r => r.operating_speed_mhz ? `${r.operating_speed_mhz} MHz` : null },
                { header: 'Manufacturer', render: r => r.manufacturer },
                { header: 'Part #', render: r => r.part_number && <span className="font-mono text-[11px]">{r.part_number}</span> },
                { header: 'Health', render: r => <Pip value={r.health} /> },
              ]}
              empty="No populated DIMMs reported"
            />
            <SubTable
              icon={HardDrive}
              title="Drives"
              count={hw.drives?.length || 0}
              error={hw.drives_error}
              rows={hw.drives}
              cols={[
                { header: 'Drive', render: r => <span className="font-mono text-slate-400">{r.id || r.name}</span> },
                { header: 'Model', render: r => r.model || r.name },
                { header: 'Capacity', render: r => fmtBytes(r.capacity_bytes) },
                { header: 'Media', render: r => r.media_type },
                { header: 'Protocol', render: r => r.protocol },
                { header: 'Controller', render: r => <span className="text-slate-500 text-[11px]">{r.controller}</span> },
                { header: 'Health', render: r => <Pip value={r.health} /> },
              ]}
            />
            <SubTable
              icon={Network}
              title="Network interfaces"
              count={hw.nics?.length || 0}
              error={hw.nics_error}
              rows={hw.nics}
              cols={[
                { header: 'Port', render: r => <span className="font-mono text-slate-400">{r.id || r.name}</span> },
                { header: 'MAC', render: r => r.mac_address && <span className="font-mono text-[11px]">{r.mac_address}</span> },
                { header: 'Speed', render: r => r.speed_mbps ? `${r.speed_mbps >= 1000 ? (r.speed_mbps/1000)+' Gb/s' : r.speed_mbps+' Mb/s'}` : null },
                { header: 'Link', render: r => {
                    if (!r.link_status) return null
                    const cls = r.link_status === 'LinkUp'
                      ? 'bg-lime-500/10 text-lime-300 ring-lime-500/30'
                      : 'bg-slate-500/10 text-slate-400 ring-slate-500/30'
                    return <span className={`inline-block px-1.5 py-0.5 rounded ${cls} ring-1 text-[10px] font-medium`}>{r.link_status}</span>
                  } },
                { header: 'IPv4', render: r => r.ipv4?.length ? <span className="font-mono text-[11px]">{r.ipv4.join(', ')}</span> : null },
                { header: 'Health', render: r => <Pip value={r.health} /> },
              ]}
            />
          </>
        )}
      />

      {/* Thermal & power — fans, temperature probes, PSUs, live
          consumption. Chassis-level readings; lazy-loaded just like
          Hardware. ~1-2s warm. */}
      <ExpandableSection
        icon={Gauge}
        title="Thermal & power"
        subtitle="Fans, temperatures, PSUs, consumption"
        load={() => api.get(`/servers/${id}/health`).then(r => r.data)}
        render={(h) => (
          <>
            {/* Consumption summary card — one of the most useful datapoints
                for a datacenter admin. Shown above the detail tables. */}
            {(h.power?.consumed_watts > 0 || h.psus?.length > 0) && (
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-5">
                <Stat label="Current draw" value={h.power?.consumed_watts ? `${h.power.consumed_watts} W` : null} tone="brand" />
                <Stat label="Average" value={h.power?.average_consumed_watts ? `${h.power.average_consumed_watts} W` : null} />
                <Stat label="Peak (window)" value={h.power?.max_consumed_watts ? `${h.power.max_consumed_watts} W` : null} />
                <Stat label="Capacity" value={h.power?.capacity_watts ? `${h.power.capacity_watts} W` : null} />
              </div>
            )}
            <SubTable
              icon={Fan}
              title="Fans"
              count={h.fans?.length || 0}
              error={h.thermal_error}
              rows={h.fans}
              cols={[
                { header: 'Fan', render: r => r.name },
                { header: 'Reading', render: r => r.reading ? `${r.reading} ${r.reading_units || ''}`.trim() : null },
                { header: 'Health', render: r => <Pip value={r.health} /> },
                { header: 'State', render: r => <span className="text-slate-500 text-[11px]">{r.state}</span> },
              ]}
            />
            <SubTable
              icon={Thermometer}
              title="Temperatures"
              count={h.temperatures?.length || 0}
              error={h.thermal_error}
              rows={h.temperatures}
              cols={[
                { header: 'Sensor', render: r => r.name },
                { header: 'Reading', render: r => {
                    if (r.reading_celsius == null) return null
                    const warn = r.upper_warning_celsius && r.reading_celsius >= r.upper_warning_celsius
                    const crit = r.upper_critical_celsius && r.reading_celsius >= r.upper_critical_celsius
                    const cls = crit ? 'text-red-300 font-semibold'
                              : warn ? 'text-amber-300 font-semibold'
                              : 'text-slate-200'
                    return <span className={cls}>{r.reading_celsius.toFixed(1)} °C</span>
                  } },
                { header: 'Warn at', render: r => r.upper_warning_celsius ? <span className="text-slate-500 text-[11px]">{r.upper_warning_celsius.toFixed(0)} °C</span> : null },
                { header: 'Critical at', render: r => r.upper_critical_celsius ? <span className="text-slate-500 text-[11px]">{r.upper_critical_celsius.toFixed(0)} °C</span> : null },
                { header: 'Health', render: r => <Pip value={r.health} /> },
              ]}
            />
            <SubTable
              icon={Wind}
              title="Power supplies"
              count={h.psus?.length || 0}
              error={h.power_error}
              rows={h.psus}
              cols={[
                { header: 'PSU', render: r => r.name },
                { header: 'Capacity', render: r => r.power_capacity_watts ? `${r.power_capacity_watts} W` : null },
                { header: 'Input', render: r => r.line_input_voltage ? `${r.line_input_voltage.toFixed(0)} V ${r.input_type || ''}`.trim() : r.input_type },
                { header: 'Manufacturer', render: r => r.manufacturer },
                { header: 'Model', render: r => r.model && <span className="font-mono text-[11px]">{r.model}</span> },
                { header: 'Health', render: r => <Pip value={r.health} /> },
              ]}
            />
          </>
        )}
      />

      {/* ISO picker modal — rendered at the page root so it escapes
          any overflow/stacking context of the expandable sections.
          Shared between "Mount ISO" and "Boot from ISO". */}
      <ISOPicker
        open={picker.open}
        onClose={() => setPicker(p => ({ ...p, open: false }))}
        slots={picker.slots}
        title={picker.mode === 'boot' ? 'Boot from ISO' : 'Mount ISO'}
        confirmLabel={picker.mode === 'boot' ? 'Mount & restart' : 'Mount'}
        confirmTone={picker.mode === 'boot' ? 'danger' : 'brand'}
        warning={picker.mode === 'boot'
          ? `This will mount the ISO, set a one-shot boot override to Cd, and ${server.power_state === 'Off' ? 'power on the server' : 'force-restart the server'}. Any running workload on "${server.name}" will be interrupted.`
          : null}
        onConfirm={async (iso, slot) => {
          const endpoint = picker.mode === 'boot' ? 'boot-from-iso' : 'mount-iso'
          const r = await api.post(`/servers/${id}/${endpoint}`, { iso_id: iso.id, slot })
          if (picker.mode === 'boot' && r.data?.id) {
            // boot-from-iso returns the refreshed server row; fold it in.
            setServer(r.data)
          }
          // Force the Virtual Media section to re-fetch slots next open.
          // Caller (ExpandableSection.render) can't reload from here —
          // easiest is to refetch the server detail so the user sees
          // fresh power state, and the admin can expand/reload VM
          // manually if they want updated slot state.
          fetchOne()
        }}
      />
    </div>
  )
}

// ─────────────────────────────────────────────────────────────
// VirtualMediaBlock — renders inside the Virtual Media expandable
// section. Shows one row per BMC slot (name, types, inserted state)
// with per-slot eject + top-level Mount / Boot buttons.
//
// Lives outside ServerDetail() so the parent component stays
// readable; state (picker open, mount progress) lives in the parent.
// ─────────────────────────────────────────────────────────────
function VirtualMediaBlock({ slots, onMount, onBoot, onEject }) {
  const hasCDSlot = slots.some(s => (s.media_types || []).some(m => ['CD', 'DVD', 'CDROM'].includes(m.toUpperCase())))
  const [busyEject, setBusyEject] = useState(null)

  const doEject = async (slot) => {
    if (!confirm(`Eject media from slot ${slot}? Any in-progress OS install will be interrupted.`)) return
    setBusyEject(slot)
    try { await onEject(slot) } finally { setBusyEject(null) }
  }

  return (
    <div>
      {/* Action buttons — top of section so they're easy to reach.
          Mount is the safe variant; Boot-from-ISO is the "kick off an
          install" flow and carries the restart warning in its picker. */}
      <div className="flex flex-wrap gap-2 mb-4">
        <button
          onClick={onMount}
          disabled={!hasCDSlot}
          title={hasCDSlot ? 'Mount an ISO on a CD/DVD slot' : 'BMC exposes no CD/DVD-capable slot'}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded border text-xs font-semibold bg-canvas-700 hover:bg-canvas-600 text-slate-200 border-canvas-500 hover:border-brand-500/60 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          <Disc3 size={12} /> Mount ISO…
        </button>
        <button
          onClick={onBoot}
          disabled={!hasCDSlot}
          title={hasCDSlot ? 'Mount + boot override + reset — installs the OS' : 'BMC exposes no CD/DVD-capable slot'}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded border text-xs font-semibold bg-brand-500 hover:bg-brand-400 text-canvas-900 border-transparent disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          <Rocket size={12} /> Boot from ISO…
        </button>
      </div>

      {/* Slot table — one row per BMC virtual-media device. */}
      <SubTable
        icon={Disc3}
        title="Slots"
        count={slots.length}
        rows={slots}
        cols={[
          { header: 'Slot', render: s => <span className="font-mono text-slate-400">{s.id}</span> },
          { header: 'Name', render: s => s.name },
          { header: 'Media types', render: s => (s.media_types || []).length ? (
              <span className="text-[11px] text-slate-400">{s.media_types.join(', ')}</span>
            ) : null },
          { header: 'State', render: s => s.inserted ? (
              <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-brand-500/10 text-brand-300 ring-1 ring-brand-500/30 text-[10px] font-medium">
                <CheckCircle size={9} /> Inserted
              </span>
            ) : (
              <span className="text-slate-600 text-[11px] italic">Empty</span>
            ) },
          { header: 'Image', render: s => s.image ? (
              <span className="font-mono text-[11px] text-slate-400 break-all" title={s.image}>
                {s.image.length > 60 ? s.image.slice(0, 60) + '…' : s.image}
              </span>
            ) : null },
          { header: 'Via', render: s => s.connected_via ? <span className="text-slate-500 text-[11px]">{s.connected_via}</span> : null },
          { header: '', render: s => s.inserted ? (
              <button
                onClick={() => doEject(s.id)}
                disabled={busyEject === s.id}
                className="flex items-center gap-1 px-2 py-0.5 rounded text-red-300 hover:bg-red-900/30 border border-red-700/30 text-[11px] disabled:opacity-50"
                title="Eject"
              >
                {busyEject === s.id ? <RefreshCw size={10} className="animate-spin" /> : <XCircle size={10} />}
                Eject
              </button>
            ) : null },
        ]}
        empty="BMC reported no virtual-media slots"
      />
    </div>
  )
}

// Stat: big-number + small-label card for the power consumption summary.
// Tiny enough to live at the bottom of the file rather than pulling
// out to components/.
function Stat({ label, value, tone }) {
  const cls = tone === 'brand'
    ? 'border-brand-500/30 bg-brand-500/5'
    : 'border-canvas-500 bg-canvas-900/40'
  return (
    <div className={`rounded-lg border px-4 py-3 ${cls}`}>
      <div className="text-slate-500 text-[10px] font-semibold tracking-wider uppercase mb-0.5">{label}</div>
      <div className={`text-xl font-semibold ${tone === 'brand' ? 'text-brand-300' : 'text-slate-200'}`}>
        {value || <span className="text-slate-600 text-base">—</span>}
      </div>
    </div>
  )
}
