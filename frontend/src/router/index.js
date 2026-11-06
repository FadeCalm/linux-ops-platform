import Vue from 'vue'
import Router from 'vue-router'
import Dashboard from '@/views/Dashboard.vue'
import Servers from '@/views/Servers.vue'
import Execute from '@/views/Execute.vue'
import History from '@/views/History.vue'
import Files from '@/views/Files.vue'
import ServerExec from '@/views/ServerExec.vue'
import TerminalPick from '@/views/TerminalPick.vue'
import Terminal from '@/views/Terminal.vue'

Vue.use(Router)

export default new Router({
  mode: 'hash',
  routes: [
    { path: '/', redirect: '/dashboard' },
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
