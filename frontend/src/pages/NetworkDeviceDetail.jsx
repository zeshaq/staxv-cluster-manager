import { useEffect, useState } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import {
  ChevronLeft, ChevronDown, ChevronRight,
  Router, RefreshCw, Trash2, CheckCircle, XCircle,
  AlertTriangle, HelpCircle, Cpu, MemoryStick, Tag, Network as NetIcon,
  Thermometer, Key, Gauge, Activity,
} from 'lucide-react'
import api from '../api'
import { ROLES, roleMeta } from './networkRoles'

// Network device detail — sibling to ServerDetail but SSH-native.
// Shows identity, reachability, live health (CPU/mem/env/interfaces).
// VLAN + per-interface IP editors will land in Phase 2/3; this page
// leaves room for those sections.

// RoleSelector — badge + inline-edit dropdown. Clicking the badge
// reveals the select; change fires a POST /role, then parent state
// updates. Kept self-contained; parent passes device + onUpdated.
function RoleSelector({ role, onChange }) {
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const m = roleMeta(role)
  const Icon = m.icon

  const change = async (e) => {
    const next = e.target.value
    if (next === role) { setEditing(false); return }
    setBusy(true)
    try { await onChange(next) } finally { setBusy(false); setEditing(false) }
  }

  if (editing) {
    return (
      <select
        autoFocus
        defaultValue={role}
        onChange={change}
        onBlur={() => setEditing(false)}
        disabled={busy}
        className="bg-canvas-700 border border-brand-500/60 text-slate-100 rounded px-2 py-0.5 text-[11px] font-medium focus:outline-none"
      >
        {ROLES.map(r => (
          <option key={r.value} value={r.value}>{r.label}</option>
        ))}
      </select>
    )
  }
  return (
    <button
      onClick={() => setEditing(true)}
      title="Click to change role"
      className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-${m.color}-500/10 text-${m.color}-300 ring-1 ring-${m.color}-500/30 text-[11px] font-medium hover:bg-${m.color}-500/20 transition-colors`}
    >
      <Icon size={11} /> {m.label}
    </button>
  )
}

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

function Row({ label, value, mono = false }) {
  const v = value ?? ''
  return (
    <div className="flex items-baseline justify-between gap-4 py-2 border-b border-canvas-500 last:border-0">
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

// Lazy-load expandable — same shape as ServerDetail's. Reload button
// in header so the admin can refresh health independently of the
// full page.
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
          {data && render(data)}
        </div>
      )}
    </div>
  )
}

// Compact sub-table shared across health sections.
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

// Stat card, used in the health header to surface CPU / mem at-a-glance.
function Stat({ label, value, tone }) {
  const cls = tone === 'brand'
    ? 'border-brand-500/30 bg-brand-500/5'
    : tone === 'warn'
    ? 'border-amber-500/30 bg-amber-500/5'
    : tone === 'bad'
    ? 'border-red-500/30 bg-red-500/5'
    : 'border-canvas-500 bg-canvas-900/40'
  return (
    <div className={`rounded-lg border px-4 py-3 ${cls}`}>
      <div className="text-slate-500 text-[10px] font-semibold tracking-wider uppercase mb-0.5">{label}</div>
      <div className={`text-xl font-semibold ${tone === 'brand' ? 'text-brand-300' : tone === 'warn' ? 'text-amber-300' : tone === 'bad' ? 'text-red-300' : 'text-slate-200'}`}>
        {value || <span className="text-slate-600 text-base">—</span>}
      </div>
    </div>
  )
}

// Uptime seconds → "4w 2d 5h" compact form. Cisco's full form is
// verbose; shorthand fits header badges.
function fmtUptime(s) {
  if (!s) return null
  const units = [['w', 7*24*3600], ['d', 24*3600], ['h', 3600], ['m', 60]]
  const parts = []
  let rem = s
  for (const [label, secs] of units) {
    const n = Math.floor(rem / secs)
    if (n > 0) { parts.push(`${n}${label}`); rem -= n * secs }
    if (parts.length === 2) break
  }
  return parts.length ? parts.join(' ') : `${s}s`
}

function fmtBytes(n) {
  if (!n) return null
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, u = 0
  while (v >= 1024 && u < units.length - 1) { v /= 1024; u++ }
  return `${v.toFixed(v < 10 && u > 0 ? 2 : v < 100 ? 1 : 0)} ${units[u]}`
}

// Small status pill for interface rows — "up"/"down"/"admin down".
function LinkPill({ status }) {
  if (!status) return null
  const s = (status || '').toLowerCase()
  let cls = 'bg-slate-500/10 text-slate-400 ring-slate-500/30'
  if (s === 'up') cls = 'bg-lime-500/10 text-lime-300 ring-lime-500/30'
  else if (s.includes('admin')) cls = 'bg-slate-500/10 text-slate-400 ring-slate-500/30'
  else if (s === 'down') cls = 'bg-red-500/10 text-red-300 ring-red-500/30'
  return (
    <span className={`inline-block px-1.5 py-0.5 rounded ${cls} ring-1 text-[10px] font-medium`}>{status}</span>
  )
}

export default function NetworkDeviceDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [device, setDevice] = useState(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(null) // 'test' | 'delete'

  const fetchOne = async () => {
    setLoading(true)
    try {
      const r = await api.get(`/network-devices/${id}`)
      setDevice(r.data); setErr('')
    } catch (e) {
      setErr(e.response?.status === 404 ? 'Network device not found' : (e.response?.data?.error || 'Load failed'))
      setDevice(null)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { fetchOne() }, [id]) // eslint-disable-line

  const setRole = async (role) => {
    try {
      const r = await api.post(`/network-devices/${id}/role`, { role })
      setDevice(r.data)
    } catch (e) {
      alert(e.response?.data?.error || 'Role update failed')
    }
  }

  const test = async () => {
    setBusy('test')
    try {
      const r = await api.post(`/network-devices/${id}/test`)
      setDevice(r.data)
    } catch (e) {
      alert(e.response?.data?.error || 'Test failed')
    } finally {
      setBusy(null)
    }
  }

  const del = async () => {
    if (!confirm(`Delete "${device.name}"? The device is unaffected; credentials are erased.`)) return
    setBusy('delete')
    try {
      await api.delete(`/network-devices/${id}`)
      navigate('/network', { replace: true })
    } catch (e) {
      alert(e.response?.data?.error || 'Delete failed')
      setBusy(null)
    }
  }

  if (loading && !device) {
    return <div className="text-brand-400 text-center py-16">Loading…</div>
  }
  if (err) {
    return (
      <div className="max-w-3xl mx-auto">
        <Link to="/network" className="inline-flex items-center gap-2 text-slate-400 hover:text-brand-300 text-sm mb-6">
          <ChevronLeft size={14} /> Back to network devices
        </Link>
        <div className="bg-red-900/30 border border-red-700/50 text-red-300 rounded-xl px-5 py-4">{err}</div>
      </div>
    )
  }
  if (!device) return null

  const last = device.last_seen_at ? new Date(device.last_seen_at).toLocaleString() : '—'
  const created = new Date(device.created_at).toLocaleString()
  const updated = new Date(device.updated_at).toLocaleString()

  return (
    <div className="max-w-5xl mx-auto space-y-5">
      <Link to="/network" className="inline-flex items-center gap-2 text-slate-400 hover:text-brand-300 text-sm">
        <ChevronLeft size={14} /> Network devices
      </Link>

      {/* Header card */}
      <div className="bg-canvas-800 border border-canvas-500 rounded-xl p-6 relative overflow-hidden">
        <div className="absolute -top-20 -right-20 w-64 h-64 bg-brand-500/10 rounded-full blur-3xl pointer-events-none hero-glow" />
        <div className="relative flex items-start justify-between gap-4 flex-wrap">
          <div className="flex items-start gap-4 min-w-0">
            <div className="w-12 h-12 rounded-xl bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center flex-shrink-0">
              <Router size={22} className="text-brand-400" />
            </div>
            <div className="min-w-0">
              <h1 className="text-white text-2xl font-bold truncate">{device.name}</h1>
              <div className="flex items-center gap-2 mt-1 flex-wrap">
                <span className="text-slate-400 text-sm font-mono">
                  {device.mgmt_host}{device.mgmt_port !== 22 && `:${device.mgmt_port}`}
                </span>
                {device.model && <><span className="text-slate-600">·</span><span className="text-slate-400 text-sm">{device.model}</span></>}
                {device.version && <><span className="text-slate-600">·</span><span className="text-slate-400 text-sm font-mono">IOS {device.version}</span></>}
              </div>
              <div className="flex items-center gap-2 mt-3 flex-wrap">
                <StatusBadge status={device.status} big />
                <RoleSelector role={device.role} onChange={setRole} />
                <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-slate-500/10 text-slate-300 ring-1 ring-slate-500/30 text-[11px] font-medium uppercase tracking-wider">
                  {device.platform}
                </span>
                {device.uptime_s > 0 && (
                  <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-brand-500/10 text-brand-300 ring-1 ring-brand-500/30 text-[11px] font-medium">
                    <Activity size={11} /> up {fmtUptime(device.uptime_s)}
                  </span>
                )}
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <button onClick={test} disabled={busy !== null}
                    className="flex items-center gap-2 bg-canvas-700 hover:bg-canvas-600 text-slate-300 px-3.5 py-2 rounded-lg text-sm font-medium border border-canvas-500 disabled:opacity-50 transition-colors">
              <RefreshCw size={13} className={busy === 'test' ? 'animate-spin' : ''} />
              {busy === 'test' ? 'Testing…' : 'Refresh'}
            </button>
            <button onClick={del} disabled={busy !== null}
                    className="flex items-center gap-2 bg-red-900/30 hover:bg-red-900/50 text-red-300 px-3.5 py-2 rounded-lg text-sm font-medium border border-red-700/40 disabled:opacity-50 transition-colors">
              <Trash2 size={13} /> Delete
            </button>
          </div>
        </div>

        {device.status_error && (
          <div className="relative mt-5 bg-red-900/20 border border-red-900/40 rounded-lg p-3">
            <div className="text-[11px] font-semibold tracking-wider uppercase text-red-400 mb-1">Last error</div>
            <div className="text-red-200/90 text-xs font-mono break-all">{device.status_error}</div>
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
        <Section icon={Tag} title="Identification">
          <Row label="Hostname" value={device.hostname} mono />
          <Row label="Model" value={device.model} />
          <Row label="IOS version" value={device.version} mono />
          <Row label="Serial" value={device.serial} mono />
          <Row label="Platform" value={device.platform} />
          <Row label="Role" value={<RoleSelector role={device.role} onChange={setRole} />} />
        </Section>

        <Section icon={Key} title="Management">
          <Row label="SSH host" value={device.mgmt_host} mono />
          <Row label="SSH port" value={device.mgmt_port} />
          <Row label="Username" value={device.username} mono />
          <Row label="Enable secret" value={device.has_enable ? (
            <span className="inline-flex items-center gap-1.5 text-slate-400 text-xs">
              <span className="font-mono">••••••••</span>
              <span className="text-slate-600">encrypted</span>
            </span>
          ) : (
            <span className="text-slate-600 text-xs italic">not set (priv-15 on login)</span>
          )} />
          <Row label="Login password" value={
            <span className="inline-flex items-center gap-1.5 text-slate-400 text-xs">
              <span className="font-mono">••••••••</span>
              <span className="text-slate-600">encrypted at rest</span>
            </span>
          } />
        </Section>

        <Section icon={NetIcon} title="Metadata">
          <Row label="Uptime" value={device.uptime_s ? fmtUptime(device.uptime_s) : null} />
          <Row label="Last seen" value={last} />
          <Row label="Enrolled" value={created} />
          <Row label="Updated" value={updated} />
        </Section>
      </div>

      {/* Health — CPU / memory / environment / interfaces. Single GET
          fans out to four `show` commands in sequence; ~2-4s on a warm
          SSH connection. Lazy-loaded so initial paint stays fast. */}
      <ExpandableSection
        icon={Gauge}
        title="Live health"
        subtitle="CPU, memory, environment, interfaces"
        load={() => api.get(`/network-devices/${id}/health`).then(r => r.data)}
        render={(h) => {
          const memUsedPct = h.memory_total_bytes
            ? Math.round((h.memory_used_bytes / h.memory_total_bytes) * 100)
            : null
          const cpuTone = h.cpu_5sec_percent >= 80 ? 'bad' : h.cpu_5sec_percent >= 50 ? 'warn' : 'brand'
          const memTone = memUsedPct >= 90 ? 'bad' : memUsedPct >= 75 ? 'warn' : 'brand'
          return (
            <>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-5">
                <Stat label="CPU 5 sec"  value={h.cpu_5sec_percent != null ? `${h.cpu_5sec_percent}%` : null} tone={cpuTone} />
                <Stat label="CPU 1 min"  value={h.cpu_1min_percent != null ? `${h.cpu_1min_percent}%` : null} />
                <Stat label="CPU 5 min"  value={h.cpu_5min_percent != null ? `${h.cpu_5min_percent}%` : null} />
                <Stat label={h.memory_pool ? `${h.memory_pool} mem` : 'Memory'}
                      value={memUsedPct != null ? `${memUsedPct}% (${fmtBytes(h.memory_used_bytes)} / ${fmtBytes(h.memory_total_bytes)})` : null}
                      tone={memTone} />
              </div>

              {(h.cpu_error || h.memory_error) && (
                <div className="bg-amber-900/20 border border-amber-900/40 text-amber-200/90 text-[11px] rounded px-2 py-1 mb-4 font-mono break-all">
                  {h.cpu_error && `cpu: ${h.cpu_error}  `}
                  {h.memory_error && `mem: ${h.memory_error}`}
                </div>
              )}

              <SubTable
                icon={Thermometer}
                title="Environment sensors"
                count={h.env?.length || 0}
                error={h.env_error}
                rows={h.env}
                cols={[
                  { header: 'Kind',    render: s => <span className="text-[11px] uppercase tracking-wider text-slate-500">{s.kind}</span> },
                  { header: 'Name',    render: s => s.name },
                  { header: 'Reading', render: s => s.reading && <span className="font-mono text-[11px]">{s.reading}</span> },
                  { header: 'State',   render: s => {
                      const u = (s.state || '').toUpperCase()
                      const cls = u === 'OK' || u === 'NORMAL' ? 'bg-lime-500/10 text-lime-300 ring-lime-500/30'
                                : u === 'WARN' ? 'bg-amber-500/10 text-amber-300 ring-amber-500/30'
                                : u === 'BAD' || u === 'FAULT' ? 'bg-red-500/10 text-red-300 ring-red-500/30'
                                : 'bg-slate-500/10 text-slate-400 ring-slate-500/30'
                      return s.state && <span className={`inline-block px-1.5 py-0.5 rounded ${cls} ring-1 text-[10px] font-medium`}>{s.state}</span>
                    } },
                ]}
                empty="No environment sensors reported (device may not expose them)"
              />

              <SubTable
                icon={NetIcon}
                title="Interfaces"
                count={h.interfaces?.length || 0}
                error={h.interfaces_error}
                rows={h.interfaces}
                cols={[
                  { header: 'Interface', render: r => <span className="font-mono text-slate-400">{r.name}</span> },
                  { header: 'IP',        render: r => r.ip && <span className="font-mono text-[11px]">{r.ip}</span> },
                  { header: 'Method',    render: r => <span className="text-[11px] text-slate-500">{r.method}</span> },
                  { header: 'Status',    render: r => <LinkPill status={r.status} /> },
                  { header: 'Protocol',  render: r => <LinkPill status={r.protocol} /> },
                ]}
              />
            </>
          )
        }}
      />
    </div>
  )
}
