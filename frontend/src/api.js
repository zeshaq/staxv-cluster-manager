import axios from 'axios'

// baseURL = '/api' — every call is relative. The Vite dev proxy
// (vite.config.js) sends these to http://localhost:5002 in dev;
// in production the React bundle is served by the Go binary itself
// and same-origin works out of the box.
const api = axios.create({ baseURL: '/api', withCredentials: true })

// Auto-redirect to /login on 401. Belt-and-braces — individual pages
// also handle auth errors, but this catches anything that falls
// through (expired cookie, backend restart, etc.).
api.interceptors.response.use(
  r => r,
  err => {
    if (err.response?.status === 401 && window.location.pathname !== '/login') {
      window.location.href = '/login'
    }
    return Promise.reject(err)
  }
)

export default api
