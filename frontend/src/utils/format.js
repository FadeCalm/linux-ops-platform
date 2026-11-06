export function formatTime(iso) {
  if (!iso) return '-'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  const pad = (n) => String(n).padStart(2, '0')
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  )
}

export function shortId(id) {
  return id ? id.slice(0, 8) : '-'
}

export function statusClass(status) {
  return `st-${status || 'UNKNOWN'}`
}

export function statusText(status) {
  const map = {
    PENDING: '等待中',
    DISPATCHING: '派发中',
    RUNNING: '运行中',
    SUCCESS: '成功',
    FAILED: '失败',
    TIMEOUT: '超时',
    CANCELLED: '已取消',
    UNKNOWN: '未知',
    online: '在线',
    offline: '离线',
    unknown: '未知'
  }
  return map[status] || status || '未知'
}

export function isTerminal(status) {
  return ['SUCCESS', 'FAILED', 'TIMEOUT', 'CANCELLED', 'UNKNOWN'].includes(status)
}

export function decodeBase64(b64) {
  if (!b64) return ''
  try {
    const bin = atob(b64)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    return new TextDecoder('utf-8').decode(bytes)
  } catch (e) {
    return ''
  }
}

export function formatDuration(ms) {
  if (!ms || ms < 0) return '-'
  if (ms < 1000) return `${ms} ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(2)} s`
  const m = Math.floor(ms / 60000)
  const s = ((ms % 60000) / 1000).toFixed(0)
  return `${m}m ${s}s`
}
