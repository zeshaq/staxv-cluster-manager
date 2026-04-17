import { useEffect, useRef, useState } from 'react'
import {
  Disc3, Upload, Download, Trash2, RefreshCw, X, Link as LinkIcon,
  CheckCircle, AlertTriangle, HourglassIcon, Copy, Check,
  FileCog, Terminal, Monitor,
} from 'lucide-react'
import api from '../api'

// ──────────────────────────────────────────────────────────────
// Status pill for each ISO row.
// uploading | downloading | ready | error → colored badge.
// ──────────────────────────────────────────────────────────────
function StatusBadge({ status }) {
  const v = {
    ready:       { icon: CheckCircle,    color: 'lime',  label: 'Ready',       spin: false },
    uploading:   { icon: Upload,         color: 'amber', label: 'Uploading',   spin: false },
    downloading: { icon: Download,       color: 'amber', label: 'Downloading', spin: true  },
    error:       { icon: AlertTriangle,  color: 'red',   label: 'Error',       spin: false },
  }[status] || { icon: HourglassIcon, color: 'slate', label: status || 'Unknown', spin: false }
  const Icon = v.icon
  return (
    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-${v.color}-500/10 text-${v.color}-300 ring-1 ring-${v.color}-500/30 text-[11px] font-medium`}>
      <Icon size={11} className={v.spin ? 'animate-spin' : ''} /> {v.label}
    </span>
  )
}

// OS-type icon mapping. Purely cosmetic — helps admin scan the list.
function OSIcon({ type }) {
  const Icon = { linux: Terminal, windows: Monitor, esxi: FileCog }[type] || Disc3
  return <Icon size={18} className="text-brand-400" />
}

// Format bytes as short human string — same helper pattern as
// ServerDetail, inlined here to keep pages independent.
function fmtBytes(n) {
  if (!n || n === 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, u = 0
  while (v >= 1000 && u < units.length - 1) { v /= 1000; u++ }
  return `${v.toFixed(v < 10 && u > 0 ? 2 : v < 100 ? 1 : 0)} ${units[u]}`
}

// ──────────────────────────────────────────────────────────────
// Add-ISO panel — toggles between Upload and Import-by-URL tabs.
// Upload is a multipart form streaming in-flight to the backend;
// URL import kicks off a server-side download, then admin polls.
// ──────────────────────────────────────────────────────────────
function AddPanel({ open, onClose, onAdded }) {
  const [tab, setTab] = useState('upload')  // 'upload' | 'url'
  if (!open) return null

  return (
    <div className="bg-canvas-800 border border-brand-500/40 rounded-xl p-5 ring-1 ring-brand-500/20">
      <div className="flex items-start justify-between mb-4">
        <div>
          <h3 className="text-slate-100 font-semibold">Add ISO</h3>
          <p className="text-slate-400 text-xs mt-0.5">
            Upload from your laptop, or import from a URL (mirror download runs on the cluster-manager host).
          </p>
        </div>
        <button onClick={onClose} className="p-1.5 rounded text-slate-500 hover:text-slate-300 hover:bg-canvas-700">
          <X size={14} />
        </button>
      </div>

      {/* Tab switcher */}
      <div className="flex gap-1 mb-5 border-b border-canvas-500">
        {[
          { k: 'upload', label: 'Upload', icon: Upload },
          { k: 'url',    label: 'Import from URL', icon: LinkIcon },
        ].map(({ k, label, icon: Icon }) => (
          <button
            key={k}
            onClick={() => setTab(k)}
            className={`flex items-center gap-2 px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
              tab === k
                ? 'text-brand-300 border-brand-500'
                : 'text-slate-500 border-transparent hover:text-slate-300'
            }`}
          >
            <Icon size={13} /> {label}
          </button>
        ))}
      </div>

      {tab === 'upload' ? <UploadForm onAdded={onAdded} onClose={onClose} /> : <ImportForm onAdded={onAdded} onClose={onClose} />}
    </div>
  )
}

function UploadForm({ onAdded, onClose }) {
  const [file, setFile] = useState(null)
  const [name, setName] = useState('')
  const [osType, setOsType] = useState('linux')
  const [osVersion, setOsVersion] = useState('')
  const [description, setDescription] = useState('')
  const [progress, setProgress] = useState(0)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const fileRef = useRef(null)

  const submit = async (e) => {
    e.preventDefault()
    setErr('')
    if (!file) { setErr('Select a file first'); return }
    if (!/\.iso$/i.test(file.name)) { setErr('Only .iso files are supported'); return }

    // Order matters — MultipartReader on the backend processes parts
    // in order, expecting metadata fields BEFORE the 'file' part so
    // CreateISO can carry them. FormData.append preserves insertion
    // order.
    const fd = new FormData()
    fd.append('name',        name || file.name)
    fd.append('os_type',     osType)
    fd.append('os_version',  osVersion)
    fd.append('description', description)
    fd.append('file',        file, file.name)

    setBusy(true); setProgress(0)
    try {
      await api.post('/isos/upload', fd, {
        onUploadProgress: (e) => {
          if (e.total) setProgress(Math.round((e.loaded / e.total) * 100))
        },
      })
      onAdded()
      onClose()
    } catch (e) {
      setErr(e.response?.data?.error || 'Upload failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="grid grid-cols-1 md:grid-cols-2 gap-3">
      {err && (
        <div className="md:col-span-2 bg-red-900/40 border border-red-700/50 text-red-300 text-sm rounded-md px-3 py-2">{err}</div>
      )}

      {/* File picker — clickable drop zone */}
      <div className="md:col-span-2">
        <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-400 mb-1.5">
          ISO file
        </label>
        <button
          type="button"
          onClick={() => fileRef.current?.click()}
          className="w-full bg-canvas-900 border border-dashed border-canvas-500 hover:border-brand-500/60 rounded px-4 py-8 text-left transition-colors group"
        >
          <div className="flex items-center gap-4">
            <div className="w-11 h-11 rounded bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center flex-shrink-0 group-hover:bg-brand-500/20">
              <Disc3 size={22} className="text-brand-400" />
            </div>
            <div className="min-w-0">
              {file ? (
                <>
                  <div className="text-slate-200 text-sm font-medium truncate">{file.name}</div>
                  <div className="text-slate-500 text-xs font-mono">{fmtBytes(file.size)}</div>
                </>
              ) : (
                <>
                  <div className="text-slate-300 text-sm">Choose an ISO file</div>
                  <div className="text-slate-500 text-xs">Up to 20 GB. Streams to disk while uploading.</div>
                </>
              )}
            </div>
          </div>
        </button>
        <input
          ref={fileRef}
          type="file"
          accept=".iso"
          className="hidden"
          onChange={(e) => {
            const f = e.target.files?.[0]
            setFile(f || null)
            if (f && !name) setName(f.name.replace(/\.iso$/i, ''))
          }}
        />
      </div>

      <Field label="Display name (optional)" value={name} onChange={setName} placeholder="Ubuntu 24.04 LTS Server" />
      <OSTypeSelect value={osType} onChange={setOsType} />
      <Field label="Version (optional)" value={osVersion} onChange={setOsVersion} placeholder="24.04 LTS" />
      <Field label="Description (optional)" value={description} onChange={setDescription} placeholder="Server install ISO" />

      {busy && (
        <div className="md:col-span-2">
          <div className="h-2 bg-canvas-900 rounded-full overflow-hidden border border-canvas-500">
            <div className="h-full bg-brand-500 transition-all" style={{ width: `${progress}%` }} />
          </div>
          <div className="text-slate-500 text-[11px] mt-1 text-right tabular-nums">{progress}%</div>
        </div>
      )}

      <div className="md:col-span-2 flex items-center justify-end gap-2 pt-2">
        <button type="button" onClick={onClose} disabled={busy}
                className="px-4 py-2 rounded text-slate-400 hover:text-slate-200 text-sm">
          Cancel
        </button>
        <button type="submit" disabled={busy || !file}
                className="flex items-center gap-2 bg-brand-500 hover:bg-brand-400 disabled:opacity-50 text-canvas-900 font-semibold px-4 py-2 rounded text-sm transition-colors">
          <Upload size={13} /> {busy ? `Uploading ${progress}%…` : 'Upload'}
        </button>
      </div>
    </form>
  )
}

function ImportForm({ onAdded, onClose }) {
  const [url, setUrl] = useState('')
  const [name, setName] = useState('')
  const [filename, setFilename] = useState('')
  const [osType, setOsType] = useState('linux')
  const [osVersion, setOsVersion] = useState('')
  const [description, setDescription] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e) => {
    e.preventDefault()
    setErr('')
    setBusy(true)
    try {
      await api.post('/isos/import', {
        url, name, filename,
        os_type: osType, os_version: osVersion, description,
      })
      onAdded()
      onClose()
    } catch (e) {
      setErr(e.response?.data?.error || 'Import failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="grid grid-cols-1 md:grid-cols-2 gap-3">
      {err && (
        <div className="md:col-span-2 bg-red-900/40 border border-red-700/50 text-red-300 text-sm rounded-md px-3 py-2">{err}</div>
      )}

      <div className="md:col-span-2">
        <Field
          label="Download URL"
          value={url}
          onChange={setUrl}
          placeholder="https://releases.ubuntu.com/24.04/ubuntu-24.04.1-live-server-amd64.iso"
          required
        />
        <p className="text-[11px] text-slate-500 mt-1.5">
          The cluster-manager host downloads the file and computes SHA-256 on the fly.
          Runs in the background; you can close this dialog and check the list.
        </p>
      </div>

      <Field label="Display name (optional)" value={name} onChange={setName} placeholder="Ubuntu 24.04 LTS" />
      <Field label="Filename (optional)" value={filename} onChange={setFilename} placeholder="auto-derived from URL" />
      <OSTypeSelect value={osType} onChange={setOsType} />
      <Field label="Version (optional)" value={osVersion} onChange={setOsVersion} placeholder="24.04" />
      <div className="md:col-span-2">
        <Field label="Description (optional)" value={description} onChange={setDescription} placeholder="Server install ISO" />
      </div>

      <div className="md:col-span-2 flex items-center justify-end gap-2 pt-2">
        <button type="button" onClick={onClose} disabled={busy}
                className="px-4 py-2 rounded text-slate-400 hover:text-slate-200 text-sm">
          Cancel
        </button>
        <button type="submit" disabled={busy || !url}
                className="flex items-center gap-2 bg-brand-500 hover:bg-brand-400 disabled:opacity-50 text-canvas-900 font-semibold px-4 py-2 rounded text-sm transition-colors">
          <Download size={13} /> {busy ? 'Queueing…' : 'Start import'}
        </button>
      </div>
    </form>
  )
}

function OSTypeSelect({ value, onChange }) {
  return (
    <div>
      <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-400 mb-1.5">OS type</label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-3 py-2 text-sm focus:outline-none"
      >
        <option value="linux">Linux</option>
        <option value="windows">Windows</option>
        <option value="esxi">VMware ESXi</option>
        <option value="other">Other</option>
      </select>
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
        onChange={(e) => onChange(e.target.value)}
        className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-3 py-2 text-sm focus:outline-none"
        {...rest}
      />
    </div>
  )
}

// ──────────────────────────────────────────────────────────────
// One ISO row — card layout to match the Servers page.
// ──────────────────────────────────────────────────────────────
function ISORow({ iso, onChanged }) {
  const [busy, setBusy] = useState(null)  // 'delete'
  const [copied, setCopied] = useState(false)

  const del = async () => {
    const msg = iso.status === 'downloading'
      ? `Cancel download of "${iso.name}" and remove the partial file?`
      : `Delete "${iso.name}"? The file on disk is removed.`
    if (!confirm(msg)) return
    setBusy('delete')
    try {
      await api.delete(`/isos/${iso.id}`)
      onChanged()
    } catch (e) {
      alert(e.response?.data?.error || 'Delete failed')
    } finally {
      setBusy(null)
    }
  }

  // Absolute BMC-facing URL. Used when an admin wants to paste it into
  // a BMC's Virtual Media UI by hand (future work will wire this up
  // through Redfish's VirtualMedia.InsertMedia action directly).
  const serveUrl = `${window.location.origin}/iso/${iso.id}/${iso.filename}`

  const copyUrl = () => {
    navigator.clipboard.writeText(serveUrl).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  const created = new Date(iso.created_at).toLocaleString()

  return (
    <div className="bg-canvas-800 border border-canvas-500 rounded-xl p-5 hover:border-brand-500/40 transition-colors">
      <div className="flex items-start gap-4">
        <div className="w-11 h-11 rounded-lg bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center flex-shrink-0">
          <OSIcon type={iso.os_type} />
        </div>

        <div className="flex-1 min-w-0">
          <div className="flex items-start justify-between gap-3 flex-wrap">
            <div className="min-w-0">
              <div className="text-slate-100 font-semibold truncate">{iso.name}</div>
              <div className="text-slate-500 text-xs font-mono truncate">{iso.filename}</div>
            </div>
            <StatusBadge status={iso.status} />
          </div>

          {/* Metadata line: OS type · version · size · sha256 prefix */}
          <div className="flex items-center gap-x-3 gap-y-1 mt-2 flex-wrap text-[11px] text-slate-500">
            <span className="uppercase tracking-wider font-medium text-slate-400">{iso.os_type}</span>
            {iso.os_version && <><span>·</span><span>{iso.os_version}</span></>}
            <span>·</span>
            <span className="font-mono">{fmtBytes(iso.size_bytes)}</span>
            {iso.sha256 && (
              <>
                <span>·</span>
                <span className="font-mono text-slate-600" title={`sha256: ${iso.sha256}`}>
                  {iso.sha256.slice(0, 12)}…
                </span>
              </>
            )}
          </div>

          {iso.description && (
            <div className="mt-2 text-slate-400 text-sm truncate">{iso.description}</div>
          )}

          {iso.source_url && (
            <div className="mt-1.5 text-[11px] text-slate-500 truncate">
              <LinkIcon size={10} className="inline mr-1 -mt-0.5" />
              <span className="font-mono">{iso.source_url}</span>
            </div>
          )}

          {iso.error && (
            <div className="mt-2 text-[11px] text-red-300/80 bg-red-900/20 border border-red-900/40 rounded px-2 py-1.5 font-mono break-all">
              {iso.error}
            </div>
          )}

          {/* Footer row: created time + action buttons */}
          <div className="flex items-center justify-between mt-3 pt-3 border-t border-canvas-500 gap-2">
            <span className="text-[10px] text-slate-600 tracking-wider uppercase">Added {created}</span>
            <div className="flex items-center gap-1.5">
              {iso.status === 'ready' && (
                <button
                  onClick={copyUrl}
                  title={`BMC-facing URL: ${serveUrl}`}
                  className="flex items-center gap-1 px-2.5 py-1 rounded text-slate-400 hover:text-brand-300 hover:bg-canvas-700 text-xs font-medium transition-colors"
                >
                  {copied ? <Check size={12} className="text-lime-400" /> : <Copy size={12} />}
                  {copied ? 'Copied' : 'Copy URL'}
                </button>
              )}
              <button
                onClick={del}
                disabled={busy !== null}
                title={iso.status === 'downloading' ? 'Cancel download + delete' : 'Delete'}
                className="p-1.5 rounded text-slate-500 hover:text-red-400 hover:bg-canvas-700 disabled:opacity-50"
              >
                <Trash2 size={12} />
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

// ──────────────────────────────────────────────────────────────
// Page
// ──────────────────────────────────────────────────────────────
export default function ISOs() {
  const [isos, setIsos] = useState([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [adding, setAdding] = useState(false)

  const fetchAll = () => {
    setLoading(true)
    api.get('/isos')
      .then(r => setIsos(r.data.isos || []))
      .catch(e => setErr(e.response?.data?.error || 'Failed to load ISOs'))
      .finally(() => setLoading(false))
  }
  useEffect(fetchAll, [])

  // Auto-refresh while any ISO is in-flight — download progress is
  // backend-side so the admin needs the list to re-poll to see
  // status transition to 'ready'. 3s keeps load trivial.
  useEffect(() => {
    const pending = isos.some(i => i.status === 'downloading' || i.status === 'uploading')
    if (!pending) return
    const t = setInterval(fetchAll, 3000)
    return () => clearInterval(t)
  }, [isos])

  return (
    <div className="max-w-7xl mx-auto space-y-5">
      <div className="flex items-end justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-white text-2xl font-bold">ISO Library</h1>
          <p className="text-slate-400 text-sm mt-1">
            Install media for bare-metal OS deployment — uploaded from your laptop or imported from a URL, mounted by BMCs via Virtual Media.
          </p>
        </div>
        {!adding && (
          <button onClick={() => setAdding(true)}
                  className="flex items-center gap-2 bg-brand-500 hover:bg-brand-400 text-canvas-900 px-4 py-2.5 rounded-lg text-sm font-semibold transition-colors shadow-lg shadow-brand-500/20">
            <Upload size={14} /> Add ISO
          </button>
        )}
      </div>

      <AddPanel open={adding} onClose={() => setAdding(false)} onAdded={fetchAll} />

      {err && (
        <div className="bg-red-900/40 border border-red-700/50 text-red-300 text-sm rounded-md px-4 py-3 flex items-center justify-between">
          <span>{err}</span>
          <button onClick={fetchAll} className="flex items-center gap-1.5 text-red-200 hover:text-white text-xs">
            <RefreshCw size={12} /> Retry
          </button>
        </div>
      )}

      {loading && isos.length === 0 ? (
        <div className="text-brand-400 text-center py-12">Loading…</div>
      ) : isos.length === 0 && !adding ? (
        <div className="bg-canvas-800 border border-dashed border-canvas-500 rounded-xl p-10 text-center">
          <div className="w-14 h-14 rounded-xl bg-brand-500/10 ring-1 ring-brand-500/30 flex items-center justify-center mx-auto mb-4">
            <Disc3 size={24} className="text-brand-400" />
          </div>
          <h3 className="text-slate-100 font-semibold text-lg mb-1">No ISOs yet</h3>
          <p className="text-slate-400 text-sm max-w-md mx-auto mb-5">
            Upload an OS install ISO from your laptop, or have the cluster-manager fetch one from a distro mirror.
          </p>
          <button onClick={() => setAdding(true)}
                  className="inline-flex items-center gap-2 bg-brand-500 hover:bg-brand-400 text-canvas-900 font-semibold px-4 py-2.5 rounded text-sm transition-colors">
            <Upload size={14} /> Add first ISO
          </button>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {isos.map(iso => (
            <ISORow key={iso.id} iso={iso} onChanged={fetchAll} />
          ))}
        </div>
      )}
    </div>
  )
}
