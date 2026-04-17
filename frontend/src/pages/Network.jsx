import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Router, Plus, Trash2, RefreshCw, X, CheckCircle, XCircle, AlertTriangle, HelpCircle,
  Eye, EyeOff, ChevronRight,
} from 'lucide-react'
import api from '../api'
import { ROLES, roleMeta } from './networkRoles'

// Network page — Cisco IOS / IOS-XE switches + routers managed over
// SSH. Shape deliberately mirrors Servers.jsx so users switching
// between pages can predict the interactions.

// ──────────────────────────────────────────────────────────────
// Status pill — reachable / unreachable / error / unknown.
// Duplicated with Servers.jsx (intentional: lets each page evolve
// independently without a shared-component coupling).
// ──────────────────────────────────────────────────────────────
// Role badge — small, icon + label. Color from the role metadata so
// router/switch/l3-switch read at a glance.
function RoleBadge({ role }) {
  const m = roleMeta(role)
  const Icon = m.icon
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-${m.color}-500/10 text-${m.color}-300 ring-1 ring-${m.color}-500/30 text-[11px] font-medium`}>
      <Icon size={11} /> {m.label}
    </span>
  )
}

function StatusBadge({ status }) {
  const variants = {
    reachable:   { icon: CheckCircle, text: 'text-lime-300',  bg: 'bg-lime-500/10',  ring: 'ring-lime-500/30',  label: 'Reachable' },
    unreachable: { icon: XCircle,     text: 'text-red-300',   bg: 'bg-red-500/10',   ring: 'ring-red-500/30',   label: 'Unreachable' },
    error:       { icon: AlertTriangle, text: 'text-amber-300', bg: 'bg-amber-500/10', ring: 'ring-amber-500/30', label: 'Error' },
    unknown:     { icon: HelpCircle,  text: 'text-slate-400', bg: 'bg-slate-500/10', ring: 'ring-slate-500/30', label: 'Unknown' },
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
// Enroll form — name, mgmt host/port, login, optional enable.
// Platform auto-detect is default; the form shows the override
// hidden behind "Advanced" so the common case is two-click.
// ──────────────────────────────────────────────────────────────
function EnrollPanel({ open, onClose, onEnrolled }) {
  const [form, setForm] = useState({
    name: '', mgmt_host: '', mgmt_port: '22',
    username: '', password: '', enable_password: '',
    platform: '', role: '',
  })
  const [showPass, setShowPass] = useState(false)
  const [showEnable, setShowEnable] = useState(false)
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(false)

  if (!open) return null
  const set = (k, v) => setForm(p => ({ ...p, [k]: v }))

  const submit = async e => {
    e.preventDefault()
    setErr(''); setLoading(true)
    try {
      const body = { ...form, mgmt_port: parseInt(form.mgmt_port, 10) || 22 }
      // Don't send empty fields — let backend default them.
      if (!body.platform) delete body.platform
      if (!body.enable_password) delete body.enable_password
      if (!body.role) delete body.role
      await api.post('/network-devices', body)
      setForm({
        name: '', mgmt_host: '', mgmt_port: '22',
        username: '', password: '', enable_password: '',
        platform: '', role: '',
      })
      onEnrolled(); onClose()
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
          <h3 className="text-slate-100 font-semibold">Enroll network device</h3>
          <p className="text-slate-400 text-xs mt-0.5">
            We'll SSH in with the credentials, run <span className="font-mono text-slate-300">show version</span>, and mark the device reachable / unreachable.
          </p>
        </div>
        <button onClick={onClose} className="p-1.5 rounded text-slate-500 hover:text-slate-300 hover:bg-canvas-700">
          <X size={14} />
        </button>
      </div>

      {err && (
        <div className="bg-red-900/40 border border-red-700/50 text-red-300 text-sm rounded-md px-3 py-2 mb-4 font-mono break-all">{err}</div>
      )}

      <form onSubmit={submit} className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Field label="Name" value={form.name} onChange={v => set('name', v)}
               placeholder="core-sw-1" required />
        <Field label="Management host" value={form.mgmt_host} onChange={v => set('mgmt_host', v)}
               placeholder="10.0.0.1  or  core-sw-1.mgmt" required />
        <Field label="SSH port" value={form.mgmt_port} onChange={v => set('mgmt_port', v)}
               placeholder="22" />
        <Field label="Username" value={form.username} onChange={v => set('username', v)}
               placeholder="admin" autoComplete="username" required />

        {/* Login password — shown/hidden toggle */}
        <div>
          <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-400 mb-1.5">Password</label>
          <div className="relative">
            <input
              type={showPass ? 'text' : 'password'}
              value={form.password}
              onChange={e => set('password', e.target.value)}
              required autoComplete="new-password"
              className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-3 py-2 pr-10 text-sm font-mono focus:outline-none"
            />
            <button type="button" onClick={() => setShowPass(!showPass)} tabIndex={-1}
                    className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300">
              {showPass ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
          </div>
        </div>

        {/* Enable secret — optional; many modern AAA setups auto-elevate */}
        <div>
          <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-400 mb-1.5">
            Enable secret <span className="text-slate-600 font-normal">(optional)</span>
          </label>
          <div className="relative">
            <input
              type={showEnable ? 'text' : 'password'}
              value={form.enable_password}
              onChange={e => set('enable_password', e.target.value)}
              autoComplete="new-password"
              placeholder="Only if the box requires 'enable'"
              className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-3 py-2 pr-10 text-sm font-mono focus:outline-none"
            />
            <button type="button" onClick={() => setShowEnable(!showEnable)} tabIndex={-1}
                    className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300">
              {showEnable ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
          </div>
        </div>

        <div className="md:col-span-2">
          <button
            type="button"
            onClick={() => setShowAdvanced(v => !v)}
            className="text-slate-500 hover:text-slate-300 text-xs font-medium"
          >
            {showAdvanced ? '▾' : '▸'} Advanced
          </button>
        </div>
        {showAdvanced && (
          <>
            <div>
              <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-400 mb-1.5">
                Platform <span className="text-slate-600 font-normal">(blank = autodetect)</span>
              </label>
              <select
                value={form.platform}
                onChange={e => set('platform', e.target.value)}
                className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-3 py-2 text-sm focus:outline-none"
              >
                <option value="">Autodetect</option>
                <option value="ios">Cisco IOS (classic)</option>
                <option value="ios-xe">Cisco IOS-XE</option>
                <option value="nxos">Cisco NX-OS</option>
              </select>
            </div>
            <div>
              <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-400 mb-1.5">
                Role <span className="text-slate-600 font-normal">(blank = autodetect from model)</span>
              </label>
              <select
                value={form.role}
                onChange={e => set('role', e.target.value)}
                className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-3 py-2 text-sm focus:outline-none"
              >
                <option value="">Autodetect</option>
                {ROLES.filter(r => r.value !== 'unknown').map(r => (
                  <option key={r.value} value={r.value}>{r.label}</option>
                ))}
              </select>
            </div>
          </>
        )}

        <div className="md:col-span-2 flex items-center justify-end gap-2 pt-2">
          <button type="button" onClick={onClose}
                  className="px-4 py-2 rounded text-slate-400 hover:text-slate-200 text-sm">Cancel</button>
          <button type="submit" disabled={loading}
                  className="flex items-center gap-2 bg-brand-500 hover:bg-brand-400 disabled:opacity-50 text-canvas-900 font-semibold px-4 py-2 rounded text-sm transition-colors">
            <Plus size={13} /> {loading ? 'Enrolling…' : 'Enroll & probe'}
          </button>
        </div>
        <p className="md:col-span-2 text-[11px] text-slate-500">
          Password + enable secret are AES-256-GCM encrypted at rest. Never logged or returned over the API.
        </p>
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
// One card per device. Parallels ServerCard — identity + status +
// model/version/serial when probed + test/delete actions.
// ──────────────────────────────────────────────────────────────
function DeviceCard({ device, onChanged }) {
  const [busy, setBusy] = useState(null) // 'test' | 'delete'

  const test = async () => {
    setBusy('test')
    try {
      await api.post(`/network-devices/${device.id}/test`)
      onChanged()
    } catch (e) {
      alert(e.response?.data?.error || 'Test failed')
    } finally {
      setBusy(null)
    }
  }

  const del = async () => {
    if (!confirm(`Delete "${device.name}"? The device is unaffected; only credentials are erased.`)) return
    setBusy('delete')
    try {
      await api.delete(`/network-devices/${device.id}`)
      onChanged()
    } catch (e) {
      alert(e.response?.data?.error || 'Delete failed')
    } finally {
      setBusy(null)
    }
  }

  const last = device.last_seen_at ? new Date(device.last_seen_at).toLocaleString() : '—'

  return (
    <div className="bg-canvas-800 border border-canvas-500 rounded-xl p-5 hover:border-brand-500/40 transition-colors group">
      <div className="flex items-start justify-between mb-3 gap-3">
        <Link to={`/network/${device.id}`} className="flex items-start gap-3 min-w-0 flex-1 hover:opacity-90">
          <div className="w-10 h-10 rounded-lg bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center flex-shrink-0">
            <Router size={18} className="text-brand-400" />
          </div>
          <div className="min-w-0">
            <div className="text-slate-100 font-semibold truncate group-hover:text-brand-300 transition-colors flex items-center gap-1">
              {device.name}
              <ChevronRight size={14} className="text-slate-600 group-hover:text-brand-400 transition-colors" />
            </div>
            <div className="text-slate-500 text-xs font-mono truncate">
              {device.mgmt_host}{device.mgmt_port !== 22 && `:${device.mgmt_port}`}
            </div>
          </div>
        </Link>
        <div className="flex items-center gap-1.5 flex-wrap justify-end">
          <RoleBadge role={device.role} />
          <StatusBadge status={device.status} />
        </div>
      </div>

      {(device.model || device.version || device.serial || device.hostname) && (
        <div className="border-t border-canvas-500 pt-3 mt-3 space-y-1.5">
          {device.hostname && <Row label="Hostname" value={device.hostname} mono />}
          {device.model    && <Row label="Model"    value={device.model} />}
          {device.version  && <Row label="IOS"      value={device.version} mono />}
          {device.serial   && <Row label="Serial"   value={device.serial} mono />}
        </div>
      )}

      {device.status_error && (
        <div className="mt-3 text-[11px] text-red-300/80 bg-red-900/20 border border-red-900/40 rounded px-2 py-1.5 font-mono break-all">
          {device.status_error}
        </div>
      )}

      <div className="flex items-center justify-between mt-4 pt-3 border-t border-canvas-500">
        <span className="text-[10px] text-slate-600 tracking-wider uppercase">
          {device.platform || 'ios'} · last seen {last}
        </span>
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
export default function Network() {
  const [devices, setDevices] = useState([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [enrolling, setEnrolling] = useState(false)
  const [roleFilter, setRoleFilter] = useState('all')

  const fetchAll = () => {
    setLoading(true)
    api.get('/network-devices')
      .then(r => setDevices(r.data.devices || []))
      .catch(e => setErr(e.response?.data?.error || 'Failed to load network devices'))
      .finally(() => setLoading(false))
  }
  useEffect(fetchAll, [])

  // Filter tabs are dynamic — only show ones that have at least one
  // device (plus "All"). Keeps the bar from cluttering with empty
  // "Firewalls" / "Unknown" tabs when the fleet doesn't have any.
  const roleCounts = devices.reduce((acc, d) => {
    acc[d.role || 'unknown'] = (acc[d.role || 'unknown'] || 0) + 1
    return acc
  }, {})
  const activeTabs = [
    { value: 'all', short: 'All', count: devices.length },
    ...ROLES.filter(r => roleCounts[r.value] > 0).map(r => ({
      ...r, count: roleCounts[r.value],
    })),
  ]
  const filtered = roleFilter === 'all'
    ? devices
    : devices.filter(d => (d.role || 'unknown') === roleFilter)

  return (
    <div className="max-w-7xl mx-auto space-y-5">
      <div className="flex items-end justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-white text-2xl font-bold">Network Devices</h1>
          <p className="text-slate-400 text-sm mt-1">
            Cisco IOS / IOS-XE switches + routers managed over SSH. VLAN + interface IP management land in a follow-up.
          </p>
        </div>
        {!enrolling && (
          <button onClick={() => setEnrolling(true)}
                  className="flex items-center gap-2 bg-brand-500 hover:bg-brand-400 text-canvas-900 px-4 py-2.5 rounded-lg text-sm font-semibold transition-colors shadow-lg shadow-brand-500/20">
            <Plus size={14} /> Enroll device
          </button>
        )}
      </div>

      <EnrollPanel open={enrolling} onClose={() => setEnrolling(false)} onEnrolled={fetchAll} />

      {err && (
        <div className="bg-red-900/40 border border-red-700/50 text-red-300 text-sm rounded-md px-4 py-3">{err}</div>
      )}

      {/* Role filter bar. Hidden when there are no devices yet. */}
      {devices.length > 0 && activeTabs.length > 2 && (
        <div className="flex items-center gap-1 flex-wrap border-b border-canvas-500">
          {activeTabs.map(t => {
            const active = roleFilter === t.value
            const Icon = t.icon
            return (
              <button
                key={t.value}
                onClick={() => setRoleFilter(t.value)}
                className={`flex items-center gap-1.5 px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
                  active
                    ? 'text-brand-300 border-brand-500'
                    : 'text-slate-500 border-transparent hover:text-slate-300'
                }`}
              >
                {Icon && <Icon size={12} />}
                {t.short}
                <span className="text-[10px] text-slate-500 font-mono">{t.count}</span>
              </button>
            )
          })}
        </div>
      )}

      {loading ? (
        <div className="text-brand-400 text-center py-12">Loading…</div>
      ) : devices.length === 0 && !enrolling ? (
        <div className="bg-canvas-800 border border-dashed border-canvas-500 rounded-xl p-10 text-center">
          <div className="w-14 h-14 rounded-xl bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center mx-auto mb-4">
            <Router size={24} className="text-brand-400" />
          </div>
          <h3 className="text-slate-100 font-semibold text-lg mb-1">No network devices enrolled</h3>
          <p className="text-slate-400 text-sm max-w-md mx-auto mb-5">
            Enroll a switch or router by its management IP and SSH credentials.
            We'll run <span className="font-mono text-slate-300">show version</span> and fetch model + IOS version automatically.
          </p>
          <button onClick={() => setEnrolling(true)}
                  className="inline-flex items-center gap-2 bg-brand-500 hover:bg-brand-400 text-canvas-900 font-semibold px-4 py-2.5 rounded text-sm transition-colors">
            <Plus size={14} /> Enroll first device
          </button>
        </div>
      ) : (
        <>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {filtered.map(d => <DeviceCard key={d.id} device={d} onChanged={fetchAll} />)}
          </div>
          {filtered.length === 0 && devices.length > 0 && (
            <div className="text-slate-500 text-sm text-center py-8 italic">
              No devices in this role. Adjust the filter above.
            </div>
          )}
        </>
      )}
    </div>
  )
}
