import { Routes, Route, Navigate } from 'react-router-dom'
import { useState, useEffect } from 'react'
import api from './api'
import Layout from './components/Layout'
import Login from './pages/Login'
import Home from './pages/Home'
import Servers from './pages/Servers'
import ServerDetail from './pages/ServerDetail'
import Network from './pages/Network'
import NetworkDeviceDetail from './pages/NetworkDeviceDetail'
import Fleet from './pages/Fleet'
import ISOs from './pages/ISOs'
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
                <Route path="/servers"        element={<Servers />} />
                <Route path="/servers/:id"    element={<ServerDetail />} />
                <Route path="/network"        element={<Network />} />
                <Route path="/network/:id"    element={<NetworkDeviceDetail />} />
                <Route path="/fleet"     element={<Fleet />} />
                <Route path="/isos"      element={<ISOs />} />
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
