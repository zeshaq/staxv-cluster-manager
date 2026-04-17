import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Eye, EyeOff } from 'lucide-react'
import api from '../api'

export default function Login({ onLogin }) {
  const navigate = useNavigate()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPass, setShowPass] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async e => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      // staxv-cluster-manager auth API mirrors hypervisor's:
      // POST /api/auth/login {username, password} → 200 + cookie, or 401 {error}
      await api.post('/auth/login', { username, password })
      await onLogin()
      navigate('/', { replace: true })
    } catch (err) {
      setError(err.response?.data?.error || 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-canvas-900 flex items-center justify-center px-4 relative overflow-hidden">
      {/* Ambient background glow — teal gradient behind the card,
          contained by overflow-hidden on the parent. */}
      <div className="absolute inset-0 pointer-events-none">
        <div className="absolute top-1/2 left-1/2 w-[600px] h-[600px] -translate-x-1/2 -translate-y-1/2 rounded-full bg-brand-600/10 blur-3xl hero-glow" />
      </div>

      <div className="w-full max-w-md relative">
        <div className="bg-canvas-800 border border-canvas-500 rounded-2xl shadow-2xl p-8">
          {/* Brand */}
          <div className="flex flex-col items-center mb-8">
            <img src="/logo.svg" alt="StaxV" className="w-20 h-20 drop-shadow-lg mb-4" />
            <h1 className="text-white text-3xl font-bold leading-none">StaxV</h1>
            <p className="text-brand-400 text-xs font-semibold tracking-[0.28em] uppercase mt-1.5">
              Cluster Manager
            </p>
            <p className="text-slate-400 text-sm mt-3">Sign in to the control plane</p>
          </div>

          {error && (
            <div className="bg-red-900/40 border border-red-700/50 text-red-300 text-sm rounded-md px-4 py-3 mb-6">
              {error}
            </div>
          )}

          <form onSubmit={handleSubmit} className="space-y-5">
            <div>
              <label className="block text-xs font-semibold tracking-wider uppercase text-slate-400 mb-1.5">Username</label>
              <input
                type="text"
                value={username}
                onChange={e => setUsername(e.target.value)}
                className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 focus:outline-none rounded-md px-3 py-2.5 transition-colors"
                autoComplete="username"
                required
              />
            </div>

            <div>
              <label className="block text-xs font-semibold tracking-wider uppercase text-slate-400 mb-1.5">Password</label>
              <div className="relative">
                <input
                  type={showPass ? 'text' : 'password'}
                  value={password}
                  onChange={e => setPassword(e.target.value)}
                  className="w-full bg-canvas-900 border border-canvas-500 focus:border-brand-500 text-slate-100 focus:outline-none rounded-md px-3 py-2.5 pr-10 transition-colors"
                  autoComplete="current-password"
                  required
                />
                <button
                  type="button"
                  onClick={() => setShowPass(!showPass)}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300"
                  tabIndex={-1}
                >
                  {showPass ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
            </div>

            <button
              type="submit"
              disabled={loading}
              className="w-full bg-brand-500 hover:bg-brand-400 disabled:opacity-50 disabled:cursor-not-allowed text-canvas-900 font-semibold py-2.5 rounded-md transition-colors shadow-lg shadow-brand-500/20"
            >
              {loading ? 'Signing in…' : 'Sign In'}
            </button>
          </form>

          <p className="text-slate-600 text-[10px] tracking-wider uppercase text-center mt-6">
            staxv-cluster-manager · control plane
          </p>
        </div>
      </div>
    </div>
  )
}
