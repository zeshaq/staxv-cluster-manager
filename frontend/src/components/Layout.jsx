import { Link, useLocation } from 'react-router-dom'
import { useEffect, useState } from 'react'
import {
  LayoutDashboard, Server, Users, Settings as SettingsIcon,
  LogOut, Activity, HardDrive, Disc3,
} from 'lucide-react'
import api from '../api'

// Sidebar nav — one entry per top-level page. Icons from lucide-react.
// Group commentary inline so "why this icon" doesn't drift from the UI.
//
// Order reflects the infrastructure layering: physical boxes (Servers,
// bare metal via Redfish) → hypervisors running on them (Fleet) →
// identity + tooling (Users, Settings).
const NAV = [
  { to: '/',         label: 'Overview', icon: LayoutDashboard }, // cluster-wide snapshot
  { to: '/servers',  label: 'Servers',  icon: HardDrive },       // physical servers (iLO / iDRAC)
  { to: '/fleet',    label: 'Fleet',    icon: Server },           // enrolled hypervisors
  { to: '/isos',     label: 'ISOs',     icon: Disc3 },            // install-media library for BMC Virtual Media
  { to: '/users',    label: 'Users',    icon: Users },            // fleet-level user directory
  { to: '/settings', label: 'Settings', icon: SettingsIcon },     // per-user + system settings
]

export default function Layout({ authState, onLogout, children }) {
  const location = useLocation()
  const [hostname, setHostname] = useState('')

  // Tiny request on every page load — shows the CM host's name in the
  // sidebar so the admin knows which box they're on. Cheap, no auth
  // issues (api interceptor redirects on 401).
  useEffect(() => {
    api.get('/host').then(r => setHostname(r.data.hostname || '')).catch(() => {})
  }, [])

  return (
    <div className="flex h-screen bg-canvas-900 overflow-hidden">
      {/* ─── Sidebar ──────────────────────────────────────────── */}
      <aside className="w-60 bg-canvas-800 border-r border-canvas-500 flex flex-col flex-shrink-0">
        {/* Brand */}
        <div className="px-5 py-5 border-b border-canvas-500">
          <div className="flex items-center gap-3">
            <img src="/logo.svg" alt="StaxV" className="w-9 h-9" />
            <div className="flex flex-col leading-none">
              <span className="text-white font-bold text-lg tracking-tight">StaxV</span>
              <span className="text-brand-400 text-[10px] font-semibold tracking-[0.22em] uppercase mt-1">
                Cluster Manager
              </span>
            </div>
          </div>
          {hostname && (
            <div className="mt-3 px-2.5 py-1.5 rounded-md bg-canvas-700 border border-canvas-500 flex items-center gap-2">
              <Activity size={11} className="text-brand-400 flex-shrink-0" />
              <span className="text-slate-300 text-xs font-medium truncate" title={hostname}>
                {hostname}
              </span>
            </div>
          )}
        </div>

        {/* Nav */}
        <nav className="flex-1 px-2 py-3 space-y-0.5 overflow-y-auto">
          {NAV.map(item => {
            const active =
              item.to === '/' ? location.pathname === '/' : location.pathname.startsWith(item.to)
            const Icon = item.icon
            return (
              <Link
                key={item.to}
                to={item.to}
                className={`flex items-center gap-3 px-3 py-2.5 rounded-md text-sm font-medium transition-colors ${
                  active
                    ? 'bg-brand-900/30 text-brand-300 border border-brand-700/40'
                    : 'text-slate-400 hover:text-slate-200 hover:bg-canvas-700 border border-transparent'
                }`}
              >
                <Icon size={15} className={active ? 'text-brand-400' : 'text-slate-500'} />
                {item.label}
              </Link>
            )
          })}
        </nav>

        {/* Footer — user + logout */}
        <div className="border-t border-canvas-500 p-3">
          <div className="flex items-center gap-2 px-3 py-2">
            <div className="w-8 h-8 rounded-full bg-brand-500/20 flex items-center justify-center text-brand-300 text-xs font-bold">
              {(authState?.username || '?').slice(0, 2).toUpperCase()}
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-slate-200 text-xs font-medium truncate">{authState?.username}</div>
              <div className="text-slate-500 text-[10px] tracking-wide uppercase">
                {authState?.is_admin ? 'admin' : 'user'}
              </div>
            </div>
            <button
              onClick={onLogout}
              title="Sign out"
              className="p-1.5 rounded text-slate-500 hover:text-red-400 hover:bg-canvas-700 transition-colors"
            >
              <LogOut size={14} />
            </button>
          </div>
        </div>
      </aside>

      {/* ─── Main column ─────────────────────────────────────── */}
      <div className="flex-1 flex flex-col overflow-hidden">
        <main className="flex-1 overflow-y-auto px-8 py-6">
          {children}
        </main>
      </div>
    </div>
  )
}
