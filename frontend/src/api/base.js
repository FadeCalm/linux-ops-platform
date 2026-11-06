// Single source of truth for the API path.
//
// Two independent build-time variables (see frontend/.env.example):
//   VITE_BASE_PATH  where static assets are mounted, e.g. /ops-wxgzt/
//   VITE_API_BASE   full API base as seen by the browser, e.g.
//                   /ops-server/api/v1 (REST *and* WebSocket use this)
//
// Defaults keep the original layout: BASE_URL "/" -> API_BASE "/api/v1".
// No trailing slash, so `${API_BASE}/servers` is always well formed.

function normalizeApiBase(raw) {
  const trimmed = String(raw || '')
    .trim()
    .replace(/\/+$/, '')
  if (!trimmed) return ''
  return trimmed.startsWith('/') ? trimmed : '/' + trimmed
}

export const API_BASE = normalizeApiBase(
  import.meta.env.VITE_API_BASE || `${import.meta.env.BASE_URL}api/v1`
)

// Absolute WebSocket URL for a path under the API base, e.g.
// wsUrl('/ws/tasks/123') -> "ws://host/ops-server/api/v1/ws/tasks/123".
export function wsUrl(path) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${location.host}${API_BASE}${path}`
}
