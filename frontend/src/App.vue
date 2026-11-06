<template>
  <div class="app-shell">
    <header class="topbar">
      <div class="brand">
        <div class="brand-mark">L</div>
        <div class="brand-text">
          <span class="brand-title">Linux 集中运维平台</span>
          <span class="brand-sub">Centralized Ops · Phase 1 · No Auth</span>
        </div>
      </div>
      <nav class="top-nav">
        <router-link
          v-for="item in nav"
          :key="item.path"
          :to="item.path"
          class="top-nav-item"
          active-class="active"
        >
          <i :class="item.icon"></i><span>{{ item.title }}</span>
        </router-link>
      </nav>
      <div class="top-status">
        <span class="status-dot" :class="{ online: backendOnline }"></span>
        <span>{{ backendOnline ? '后端在线' : '后端离线' }}</span>
        <el-tooltip :content="theme === 'dark' ? '切换到浅色（白）配色' : '切换到深色（黑）配色'" placement="bottom">
          <button class="theme-toggle" type="button" @click="toggleTheme">
            <i :class="theme === 'dark' ? 'el-icon-sunny' : 'el-icon-moon'"></i>
            <span>{{ theme === 'dark' ? '浅色' : '深色' }}</span>
          </button>
        </el-tooltip>
      </div>
    </header>

    <div class="body">
      <aside class="sidebar">
        <div class="side-section">导航</div>
        <router-link
          v-for="item in nav"
          :key="item.path"
          :to="item.path"
          class="side-item"
          active-class="active"
        >
          <i :class="item.icon"></i><span>{{ item.title }}</span>
        </router-link>

        <div class="side-section" style="margin-top: 24px">环境提示</div>
        <div class="side-note">
          <div class="side-note-title"><i class="el-icon-warning-outline"></i> 隔离网络</div>
          <div class="side-note-text">
            本版本无身份认证，仅限受控内网联调，禁止暴露公网。正式生产前须完成安全加固。
          </div>
        </div>
      </aside>

      <main class="content">
        <div class="page-head">
          <h2>{{ $route.meta.title }}</h2>
          <div class="page-sub">{{ subtitle }}</div>
        </div>
        <router-view />
      </main>
    </div>
  </div>
</template>

<script>
import { applyTheme, getTheme } from '@/utils/theme'

export default {
  name: 'App',
  data() {
    return {
      theme: getTheme(),
      nav: [
        { path: '/dashboard', title: '系统概览', icon: 'el-icon-data-line' },
        { path: '/servers', title: '服务器管理', icon: 'el-icon-monitor' },
        { path: '/execute', title: '命令执行', icon: 'el-icon-cpu' },
        { path: '/files', title: '文件管理', icon: 'el-icon-folder-opened' },
        { path: '/terminal', title: '交互终端', icon: 'el-icon-connection' },
        { path: '/history', title: '任务历史', icon: 'el-icon-document' }
      ]
    }
  },
  computed: {
    backendOnline() {
      return this.$store.state.backendOnline
    },
    subtitle() {
      const map = {
        dashboard: '服务器在线状态、Agent 版本与近期任务概览',
        servers: '管理受控主机、分组、标签与 Agent 健康状态',
        execute: '选择目标主机并发执行命令，实时查看 stdout / stderr',
        files: '浏览目标主机目录，支持新建、编辑、上传、下载与删除',
        history: '按时间、主机与状态检索任务，回放日志并下载',
        'server-execute': '针对单台主机执行命令，查看实时输出与最近任务',
        terminal: '通过 PTY 打开真实交互式 Shell，支持 cd / vim / top / Ctrl+C 与窗口缩放',
        'terminal-pick': '选择一台受管主机，打开真实的交互式 Shell 会话'
      }
      return map[this.$route.name] || ''
    }
  },
  methods: {
    toggleTheme() {
      this.theme = this.theme === 'dark' ? 'light' : 'dark'
      applyTheme(this.theme)
    }
  },
  created() {
    this.$store.dispatch('loadLimits').catch(() => {})
    this.timer = setInterval(() => {
      this.$store.dispatch('loadLimits').catch(() => {})
    }, 15000)
  },
  beforeDestroy() {
    clearInterval(this.timer)
  }
}
</script>
