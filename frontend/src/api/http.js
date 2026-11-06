import axios from 'axios'
import { API_BASE } from './base'

const http = axios.create({
  baseURL: API_BASE,
  timeout: 20000
})

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
    return Promise.reject(new Error(msg))
  }
)

export default http
