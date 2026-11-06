import Vue from 'vue'
import Router from 'vue-router'
import api from '@/api'
import store from '@/store'
import Login from '@/views/Login.vue'
import Dashboard from '@/views/Dashboard.vue'
import Servers from '@/views/Servers.vue'
import Execute from '@/views/Execute.vue'
import History from '@/views/History.vue'
import Files from '@/views/Files.vue'
import ServerExec from '@/views/ServerExec.vue'
import TerminalPick from '@/views/TerminalPick.vue'
import Terminal from '@/views/Terminal.vue'

Vue.use(Router)

const router = new Router({
  mode: 'hash',
  routes: [
    { path: '/', redirect: '/dashboard' },
    { path: '/login', name: 'login', component: Login, meta: { title: '登录', public: true } },
    { path: '/dashboard', name: 'dashboard', component: Dashboard, meta: { title: '系统概览', icon: 'el-icon-data-line' } },
    { path: '/servers', name: 'servers', component: Servers, meta: { title: '服务器管理', icon: 'el-icon-monitor' } },
    { path: '/servers/:id/execute', name: 'server-execute', component: ServerExec, meta: { title: '单机执行', icon: 'el-icon-cpu' } },
    { path: '/execute', name: 'execute', component: Execute, meta: { title: '命令执行', icon: 'el-icon-cpu' } },
    { path: '/files', name: 'files', component: Files, meta: { title: '文件管理', icon: 'el-icon-folder-opened' } },
    { path: '/terminal', name: 'terminal-pick', component: TerminalPick, meta: { title: '交互终端', icon: 'el-icon-connection' } },
    { path: '/servers/:id/terminal', name: 'terminal', component: Terminal, meta: { title: '交互终端', icon: 'el-icon-connection' } },
    { path: '/history', name: 'history', component: History, meta: { title: '任务历史', icon: 'el-icon-document' } }
  ]
})

// Ask the backend on every navigation whether the session is still valid.
// The request is tiny and keeps the guard correct after the cookie expires or
// the backend is restarted with a different password.
router.beforeEach(async (to, from, next) => {
  if (to.meta && to.meta.public) {
    if (to.name !== 'login') return next()
    // Already logged in (or no password configured): skip the login page.
    try {
      const st = await api.auth.status()
      store.commit('setAuthRequired', !!st.required)
      if (!st.required || st.authenticated) return next('/dashboard')
    } catch (e) {
      // Backend unreachable: stay on the login page.
    }
    return next()
  }

  try {
    const st = await api.auth.status()
    store.commit('setAuthRequired', !!st.required)
    if (!st.required || st.authenticated) return next()
  } catch (e) {
    if (e && e.status === 401) {
      return next({ path: '/login', query: { redirect: to.fullPath } })
    }
    // Network error: let the view render its offline state.
    return next()
  }
  next({ path: '/login', query: { redirect: to.fullPath } })
})

export default router
