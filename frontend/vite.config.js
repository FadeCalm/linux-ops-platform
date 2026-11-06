import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue2'
import { fileURLToPath, URL } from 'node:url'
import fs from 'node:fs'
import path from 'node:path'

// Each build must produce exactly one layout: dist/index.html (no prefix) or
// dist/<prefix>/index.html. Remove the previous dist/ first so switching
// prefixes never leaves a stale build behind to be deployed by accident.
function cleanDist() {
  return {
    name: 'clean-dist',
    apply: 'build',
    buildStart() {
      fs.rmSync(path.resolve(process.cwd(), 'dist'), {
        recursive: true,
        force: true
      })
    }
  }
}

// VITE_BASE_PATH: where the built frontend is mounted, e.g. "ops-wxgzt"
//   -> "/ops-wxgzt/". Default "/" (unchanged behavior).
function normalizeBase(raw) {
  if (!raw || raw.trim() === '' || raw.trim() === '/') return '/'
  return '/' + raw.trim().replace(/^\/+|\/+$/g, '') + '/'
}

// VITE_API_BASE: the full path the browser uses to reach backend /api/v1,
// e.g. "/ops-server/api/v1". Defaults to "<base>api/v1".
function normalizeApiBase(raw, base) {
  const value = (raw && raw.trim()) || `${base}api/v1`
  const trimmed = value.replace(/\/+$/, '')
  return trimmed.startsWith('/') ? trimmed : '/' + trimmed
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const base = normalizeBase(env.VITE_BASE_PATH || process.env.VITE_BASE_PATH)
  const apiBase = normalizeApiBase(
    env.VITE_API_BASE || process.env.VITE_API_BASE,
    base
  )

  // Put the built app one level down, named after the prefix:
  //   VITE_BASE_PATH=/ops-wxgzt/ -> dist/ops-wxgzt/index.html
  //   unset                      -> dist/index.html (unchanged)
  // Deploying the whole dist/ then serves the app at /ops-wxgzt/ directly.
  const prefix = base === '/' ? '' : base.replace(/^\/+|\/+$/g, '')
  const outDir = prefix ? `dist/${prefix}` : 'dist'

  const proxy = {
    '/api': {
      target: 'http://127.0.0.1:8080',
      changeOrigin: true,
      ws: true
    }
  }

  // Dev only: accept the configured API base (e.g. /ops-server/api/v1) and
  // strip it down to /api/v1 before forwarding to the backend, mirroring what
  // the production Nginx does.
  if (apiBase !== '/api/v1') {
    proxy[apiBase] = {
      target: 'http://127.0.0.1:8080',
      changeOrigin: true,
      ws: true,
      rewrite: (p) => '/api/v1' + p.slice(apiBase.length)
    }
  }

  return {
    base,
    plugins: [vue(), cleanDist()],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url))
      }
    },
    server: {
      host: '127.0.0.1',
      port: 5173,
      proxy
    },
    build: {
      outDir,
      emptyOutDir: true,
      chunkSizeWarningLimit: 1500
    }
  }
})
