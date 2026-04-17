import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { execSync } from 'child_process'

const gitHash = (() => {
  try { return execSync('git rev-parse --short HEAD').toString().trim() }
  catch { return 'dev' }
})()

export default defineConfig({
  plugins: [react()],
  define: {
    __GIT_HASH__: JSON.stringify(gitHash),
  },
  server: {
    // `make frontend-dev` runs `vite` here on :5173 and proxies API to
    // the Go backend on :5002. Lets you hot-reload React while the Go
    // side is handling auth/DB/etc. via air.
    proxy: {
      '/api': 'http://localhost:5002',
    },
  },
  build: {
    outDir: 'dist',
    target: 'esnext',
  },
})
