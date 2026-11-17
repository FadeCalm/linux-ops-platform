import http from './http'
import { API_BASE } from './base'

export default {
  auth: {
    status: () => http.get('/auth/status'),
    login: (username, password) => http.post('/auth/login', { username, password }),
    logout: () => http.post('/auth/logout'),
    changePassword: (oldPassword, newPassword) =>
      http.post('/auth/password', { old_password: oldPassword, new_password: newPassword }),
    // Exchange credentials for a short-lived JWT (for scripts; the SPA itself
    // keeps using the HttpOnly session cookie).
    issueToken: (username, password) => http.post('/auth/token', { username, password })
  },

  tokens: {
    list: (userId) => http.get('/auth/tokens', { params: userId ? { user_id: userId } : {} }),
    create: (name, expiresInDays) =>
      http.post('/auth/tokens', { name, expires_in_days: expiresInDays }),
    remove: (id) => http.delete(`/auth/tokens/${id}`)
  },

  users: {
    list: () => http.get('/users'),
    create: (username, password) => http.post('/users', { username, password }),
    resetPassword: (id, password) => http.put(`/users/${id}/password`, { password }),
    remove: (id) => http.delete(`/users/${id}`)
  },

  health: () => http.get('/health'),
  limits: () => http.get('/config/limits'),
  overview: () => http.get('/stats/overview'),

  servers: {
    list: (params) => http.get('/servers', { params }),
    get: (id) => http.get(`/servers/${id}`),
    create: (data) => http.post('/servers', data),
    update: (id, data) => http.put(`/servers/${id}`, data),
    remove: (id) => http.delete(`/servers/${id}`),
    agent: (id) => http.get(`/servers/${id}/agent`),
    batchCheck: (ids) =>
      http.post('/agents/check', { server_ids: ids }, { timeout: 300000 }),
    updateCheck: (id) => http.get(`/servers/${id}/agent/update/check`),
    applyUpdate: (id) => http.post(`/servers/${id}/agent/update`, null, { timeout: 0 }),
    batchUpdateCheck: (ids) =>
      http.post('/agents/update/check', { server_ids: ids }, { timeout: 90000 }),
    batchUpdate: (ids) =>
      http.post('/agents/update', { server_ids: ids }, { timeout: 0 })
  },

  groups: {
    list: () => http.get('/groups'),
    create: (data) => http.post('/groups', data),
    update: (id, data) => http.put(`/groups/${id}`, data),
    remove: (id) => http.delete(`/groups/${id}`)
  },

  tags: {
    list: () => http.get('/tags'),
    create: (data) => http.post('/tags', data),
    remove: (id) => http.delete(`/tags/${id}`)
  },

  tasks: {
    create: (data) => http.post('/tasks', data),
    list: (params) => http.get('/tasks', { params }),
    get: (id) => http.get(`/tasks/${id}`),
    targets: (id) => http.get(`/tasks/${id}/targets`),
    cancel: (id) => http.post(`/tasks/${id}/cancel`)
  },

  targets: {
    get: (id) => http.get(`/targets/${id}`),
    logs: (id, stream, offset, limit) =>
      http.get(`/targets/${id}/logs`, { params: { stream, offset, limit } }),
    downloadUrl: (id, stream) =>
      `${API_BASE}/targets/${id}/logs/download?stream=${stream}`,
    cancel: (id) => http.post(`/targets/${id}/cancel`),
    retry: (id) => http.post(`/targets/${id}/retry`)
  },

  audit: {
    list: (params) => http.get('/audit', { params })
  },

  fs: {
    list: (serverId, path, params = {}) =>
      http.get(`/servers/${serverId}/fs/list`, { params: { path, ...params } }),
    stat: (serverId, path) =>
      http.get(`/servers/${serverId}/fs/stat`, { params: { path } }),
    read: (serverId, path, offset = 0, limit = 2 * 1024 * 1024) =>
      http.get(`/servers/${serverId}/fs/read`, { params: { path, offset, limit } }),
    write: (serverId, data) => http.put(`/servers/${serverId}/fs/write`, data),
    op: (serverId, data) => http.post(`/servers/${serverId}/fs/op`, data),
    downloadUrl: (serverId, path) =>
      `${API_BASE}/servers/${serverId}/fs/download?path=${encodeURIComponent(path)}`,
    upload: (serverId, params, blob, onProgress) =>
      http.put(`/servers/${serverId}/fs/upload`, blob, {
        params,
        timeout: 0,
        headers: { 'Content-Type': 'application/octet-stream' },
        onUploadProgress: onProgress
      })
  }
}
