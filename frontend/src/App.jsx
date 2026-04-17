import { Routes, Route, Navigate } from 'react-router-dom'
import { useState, useEffect } from 'react'
import api from './api'
import Layout from './components/Layout'
import Login from './pages/Login'
import Home from './pages/Home'
import Fleet from './pages/Fleet'
import Users from './pages/Users'
import Settings from './pages/Settings'

function PrivateRoute({ children, authState }) {
  if (authState === 'loading') {
    return (
      <div className="flex items-center justify-center h-screen bg-canvas-900">
        <div className="text-brand-400 text-lg">Loading…</div>
      </div>
    )
  }
  if (!authState.authenticated) {
    return <Navigate to="/login" replace />
  }
  return children
}

export default function App() {
  const [authState, setAuthState] = useState('loading')

  useEffect(() => {
    api.get('/auth/me')
      .then(res => setAuthState({ authenticated: true, ...res.data }))
      .catch(() => setAuthState({ authenticated: false }))
  }, [])

  const handleLogout = async () => {
    try { await api.post('/auth/logout') } catch {}
    setAuthState({ authenticated: false })
    window.location.href = '/login'
  }

  const refreshAuth = () =>
    api.get('/auth/me').then(r => setAuthState({ authenticated: true, ...r.data }))

  return (
    <Routes>
      <Route path="/login" element={<Login onLogin={refreshAuth} />} />
      <Route
        path="/*"
        element={
          <PrivateRoute authState={authState}>
            <Layout authState={authState} onLogout={handleLogout}>
              <Routes>
                <Route path="/"          element={<Home />} />
                <Route path="/fleet"     element={<Fleet />} />
                <Route path="/users"     element={<Users />} />
                <Route path="/settings"  element={<Settings />} />
                <Route path="*"          element={<Navigate to="/" replace />} />
              </Routes>
            </Layout>
          </PrivateRoute>
        }
      />
    </Routes>
  )
}
