import { useEffect, useState } from 'react'
import { Key, Plus, Trash2, Save, Eye, EyeOff, Lock } from 'lucide-react'
import api from '../api'

// Functional — wires the real /api/settings endpoints shipped in the
// scaffold. Values are AES-256-GCM encrypted at rest, scoped per user.
export default function Settings() {
  const [keys, setKeys]         = useState([])
  const [loading, setLoading]   = useState(true)
  const [error, setError]       = useState('')
  const [selected, setSelected] = useState(null)   // {key, value, updated_at}
  const [reveal, setReveal]     = useState(false)

  // New-entry form
  const [newKey, setNewKey] = useState('')
  const [newVal, setNewVal] = useState('')
  const [savingNew, setSavingNew] = useState(false)

  const loadKeys = () => {
    setLoading(true)
    api.get('/settings/')
      .then(r => setKeys(r.data.keys || []))
      .catch(e => setError(e.response?.data?.error || 'Failed to load settings'))
      .finally(() => setLoading(false))
  }
  useEffect(loadKeys, [])

  const loadValue = async (k) => {
    setReveal(false)
    setSelected({ key: k, loading: true })
    try {
      const r = await api.get(`/settings/${encodeURIComponent(k)}`)
      setSelected(r.data)
    } catch (e) {
      setSelected({ key: k, error: e.response?.data?.error || 'load failed' })
    }
  }

  const saveNew = async (e) => {
    e.preventDefault()
    if (!newKey.trim()) return
    setSavingNew(true)
    try {
      await api.put(`/settings/${encodeURIComponent(newKey.trim())}`, { value: newVal })
      setNewKey(''); setNewVal('')
      loadKeys()
    } catch (e) {
      alert(e.response?.data?.error || 'Save failed')
    } finally {
      setSavingNew(false)
    }
  }

  const deleteKey = async (k) => {
    if (!confirm(`Delete setting "${k}"? This cannot be undone.`)) return
    try {
      await api.delete(`/settings/${encodeURIComponent(k)}`)
      if (selected?.key === k) setSelected(null)
      loadKeys()
    } catch (e) {
      alert(e.response?.data?.error || 'Delete failed')
    }
  }

  return (
    <div className="max-w-6xl mx-auto space-y-6">
      <div>
        <h1 className="text-white text-2xl font-bold">Settings</h1>
        <p className="text-slate-400 text-sm mt-1 flex items-center gap-2">
          <Lock size={13} className="text-brand-400" />
          Per-user key/value store. Values are AES-256-GCM encrypted at rest.
        </p>
      </div>

      {error && (
        <div className="bg-red-900/40 border border-red-700/50 text-red-300 text-sm rounded-md px-4 py-3">
          {error}
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-5 gap-4">
        {/* ───── Key list ───── */}
        <div className="md:col-span-2 bg-canvas-800 border border-canvas-500 rounded-xl overflow-hidden">
          <div className="px-4 py-3 border-b border-canvas-500 flex items-center justify-between">
            <span className="text-slate-400 text-xs font-semibold tracking-wider uppercase">Your keys</span>
            <span className="text-slate-500 text-xs">{keys.length}</span>
          </div>
          {loading ? (
            <div className="px-4 py-6 text-slate-500 text-sm text-center">Loading…</div>
          ) : keys.length === 0 ? (
            <div className="px-4 py-6 text-slate-500 text-sm text-center">No settings yet.</div>
          ) : (
            <ul className="divide-y divide-canvas-500">
              {keys.map(k => (
                <li key={k} className={`px-4 py-2.5 flex items-center justify-between hover:bg-canvas-700 transition-colors cursor-pointer ${selected?.key === k ? 'bg-canvas-700' : ''}`}
                    onClick={() => loadValue(k)}>
                  <div className="flex items-center gap-2 min-w-0">
                    <Key size={12} className="text-brand-500 flex-shrink-0" />
                    <span className="text-slate-200 font-mono text-sm truncate">{k}</span>
                  </div>
                  <button
                    onClick={(e) => { e.stopPropagation(); deleteKey(k) }}
                    className="p-1 rounded text-slate-600 hover:text-red-400 hover:bg-canvas-600 transition-colors"
                    title="Delete"
                  >
                    <Trash2 size={13} />
                  </button>
                </li>
              ))}
            </ul>
          )}

          {/* New entry */}
          <form onSubmit={saveNew} className="border-t border-canvas-500 p-4 space-y-3 bg-canvas-900/40">
            <div>
              <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-500 mb-1">New key</label>
              <input
                value={newKey}
                onChange={e => setNewKey(e.target.value)}
                placeholder="e.g. pull_secret"
                className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-2.5 py-1.5 text-sm font-mono focus:outline-none"
              />
            </div>
            <div>
              <label className="block text-[11px] font-semibold tracking-wider uppercase text-slate-500 mb-1">Value</label>
              <textarea
                value={newVal}
                onChange={e => setNewVal(e.target.value)}
                rows={3}
                placeholder="…"
                className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 rounded px-2.5 py-1.5 text-sm font-mono focus:outline-none resize-y"
              />
            </div>
            <button
              type="submit"
              disabled={savingNew || !newKey.trim()}
              className="w-full flex items-center justify-center gap-2 bg-brand-500 hover:bg-brand-400 disabled:opacity-40 text-canvas-900 font-semibold py-1.5 rounded text-sm transition-colors"
            >
              <Plus size={13} /> {savingNew ? 'Saving…' : 'Add setting'}
            </button>
          </form>
        </div>

        {/* ───── Detail pane ───── */}
        <div className="md:col-span-3 bg-canvas-800 border border-canvas-500 rounded-xl overflow-hidden">
          <div className="px-5 py-3 border-b border-canvas-500">
            <span className="text-slate-400 text-xs font-semibold tracking-wider uppercase">Detail</span>
          </div>
          {!selected ? (
            <div className="px-5 py-12 text-slate-500 text-sm text-center">
              Select a key to view its decrypted value.
            </div>
          ) : selected.loading ? (
            <div className="px-5 py-12 text-slate-500 text-sm text-center">Loading…</div>
          ) : selected.error ? (
            <div className="px-5 py-12 text-red-400 text-sm text-center">{selected.error}</div>
          ) : (
            <div className="p-5 space-y-4">
              <div>
                <div className="text-[11px] font-semibold tracking-wider uppercase text-slate-500 mb-1">Key</div>
                <div className="text-brand-300 font-mono text-sm break-all">{selected.key}</div>
              </div>
              <div>
                <div className="flex items-center justify-between mb-1">
                  <div className="text-[11px] font-semibold tracking-wider uppercase text-slate-500">Value</div>
                  <button
                    onClick={() => setReveal(!reveal)}
                    className="flex items-center gap-1 text-[11px] text-slate-500 hover:text-brand-300 transition-colors"
                  >
                    {reveal ? <><EyeOff size={11} /> Hide</> : <><Eye size={11} /> Reveal</>}
                  </button>
                </div>
                <pre className="bg-canvas-900 border border-canvas-500 rounded px-3 py-2 text-slate-200 font-mono text-xs overflow-x-auto whitespace-pre-wrap break-all">
{reveal ? selected.value : '•'.repeat(Math.min(selected.value?.length ?? 0, 40))}
                </pre>
              </div>
              <div className="text-slate-500 text-[11px]">
                Updated {selected.updated_at ? new Date(selected.updated_at).toLocaleString() : '—'}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
