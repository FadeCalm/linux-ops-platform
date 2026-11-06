import axios from 'axios'
import { API_BASE } from './base'

const http = axios.create({
  baseURL: API_BASE,
  timeout: 20000,
  // The session is an HttpOnly cookie; harmless for same-origin requests and
  // required when the SPA is served from a different origin than the API.
  withCredentials: true
})

// Hash mode: the router reads this change and shows the login page. The
// current route is preserved so a successful login returns to it.
function redirectToLogin() {
  const current = window.location.hash || '#/dashboard'
  if (current.startsWith('#/login')) return
  const redirect = encodeURIComponent(current.replace(/^#/, ''))
  window.location.hash = `#/login?redirect=${redirect}`
}

http.interceptors.response.use(
  (response) => {
    const body = response.data
    if (body && typeof body === 'object' && Object.prototype.hasOwnProperty.call(body, 'code')) {
      if (body.code === 0) return body.data
      return Promise.reject(new Error(body.message || 'request failed'))
    }
    return body
  },
  (error) => {
    const body = error.response && error.response.data
    const msg = (body && (body.message || body.error)) || error.message || '网络错误'
    if (error.response && error.response.status === 401) {
      redirectToLogin()
    }
    const err = new Error(msg)
    err.status = error.response && error.response.status
    return Promise.reject(err)
  }
)

export default http
