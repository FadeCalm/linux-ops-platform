<template>
  <div class="terminal-workspace" :class="{ 'is-fullscreen': fullscreen }">
    <div v-loading="loading" class="panel workspace-panel">
      <div class="ws-tabs">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          type="button"
          class="ws-tab"
          :class="{ active: tab.id === activeId }"
          @click="activate(tab.id)"
        >
          <span class="ws-dot" :class="dotClass(tab.id)"></span>
          <span class="ws-tab-name">{{ tabLabel(tab) }}</span>
          <span class="ws-tab-ip mono">{{ tab.server.ip }}</span>
          <i class="el-icon-close ws-tab-close" @click.stop="closeTab(tab.id)"></i>
        </button>
        <el-button class="ws-add" size="mini" icon="el-icon-plus" @click="openPicker">新终端</el-button>
        <div class="spacer"></div>
        <el-select v-model="fontSize" size="mini" class="ws-font" title="终端字号">
          <el-option v-for="s in fontSizes" :key="s" :label="s + ' px'" :value="s" />
        </el-select>
        <el-button size="mini" icon="el-icon-refresh" :loading="refreshing" @click="refreshServers">
          刷新状态
        </el-button>
        <el-button
          size="mini"
          :icon="fullscreen ? 'el-icon-close' : 'el-icon-full-screen'"
          @click="toggleFullscreen"
        >{{ fullscreen ? '退出全屏' : '全屏' }}</el-button>
      </div>

      <div v-if="activeTab" class="ws-meta">
        <strong>{{ tabLabel(activeTab) }}</strong>
        <span class="status-badge" :class="statusClass(activeTab.server.status)">
          {{ statusText(activeTab.server.status) }}
        </span>
        <span class="mono">{{ activeTab.server.ip }}:{{ activeTab.server.agent_port || 19090 }}</span>
        <span class="muted">{{ activeTab.server.hostname || '-' }}</span>
        <span class="muted">{{ activeTab.server.os || '-' }} / {{ activeTab.server.arch || '-' }}</span>
        <span class="muted mono">agent {{ activeTab.server.agent_version || '-' }}</span>
        <span class="muted">心跳 {{ formatTime(activeTab.server.last_heartbeat_at) }}</span>
        <div class="spacer"></div>
        <el-button size="mini" icon="el-icon-cpu" @click="goExec">单机执行</el-button>
        <el-button size="mini" icon="el-icon-folder-opened" @click="goFiles">文件管理</el-button>
        <el-button size="mini" icon="el-icon-document" @click="goHistory">任务历史</el-button>
      </div>

      <div class="ws-body">
        <TerminalPane
          v-for="tab in tabs"
          :key="tab.id"
          ref="panes"
          :server="tab.server"
          :active="tab.id === activeId"
          :font-size="fontSize"
          @state="onPaneState(tab.id, $event)"
        />
        <div v-if="!tabs.length && !loading" class="ws-empty">
          <i class="el-icon-connection"></i>
          <p>还没有打开的终端</p>
          <el-button type="primary" size="small" icon="el-icon-plus" @click="openPicker">
            选择主机打开终端
          </el-button>
        </div>
      </div>
    </div>

    <el-dialog title="打开新终端" :visible.sync="pickerVisible" width="460px">
      <el-select
        v-model="pickerId"
        filterable
        remote
        :remote-method="searchPickerServers"
        :loading="pickerSearching"
        placeholder="搜索 IP / 名称"
        style="width: 100%"
      >
        <el-option
          v-for="s in servers"
          :key="s.id"
          :value="s.id"
          :label="serverLabel(s)"
        />
      </el-select>
      <div class="picker-tip muted">
        已打开的终端不会重复创建，选择后会直接切换过去。
      </div>
      <span slot="footer">
        <el-button @click="pickerVisible = false">取消</el-button>
        <el-button type="primary" :disabled="!pickerId" @click="openPicked">打开终端</el-button>
      </span>
    </el-dialog>
  </div>
</template>

<script>
import api from '@/api'
import TerminalPane from '@/components/TerminalPane.vue'
import { formatTime, statusClass, statusText } from '@/utils/format'

const TABS_KEY = 'wxgzt.terminal.tabs'
const FONT_KEY = 'wxgzt.terminal.fontSize'
const FONT_SIZES = [11, 12, 13, 14, 16]

function readSavedTabs() {
  try {
    const raw = sessionStorage.getItem(TABS_KEY)
    const list = raw ? JSON.parse(raw) : []
    return Array.isArray(list) ? list : []
  } catch (e) {
    return []
  }
}

function readFontSize() {
  try {
    const v = Number(localStorage.getItem(FONT_KEY))
    return FONT_SIZES.includes(v) ? v : 12
  } catch (e) {
    return 12
  }
}

// The terminal workspace keeps one TerminalPane per tab, all mounted, so
// several PTY sessions can stay open at once and survive tab switches. Only
// the tab list is persisted (sessionStorage): navigating away closes the PTYs,
// and returning reopens the same tabs with auto-connect.
export default {
  name: 'TerminalWorkspace',
  components: { TerminalPane },
  data() {
    return {
      loading: false,
      refreshing: false,
      tabs: [],
      activeId: null,
      paneStates: {},
      servers: [],
      pickerVisible: false,
      pickerId: null,
      pickerSearching: false,
      fullscreen: false,
      fontSize: readFontSize(),
      fontSizes: FONT_SIZES
    }
  },
  computed: {
    activeTab() {
      return this.tabs.find((t) => t.id === this.activeId) || null
    },
    connectedTabs() {
      return this.tabs.filter((t) => ['open', 'connecting'].includes(this.paneStates[t.id]))
    }
  },
  watch: {
    '$route.params.id'(next) {
      if (next) this.ensureTab(Number(next))
    },
    fontSize(v) {
      try {
        localStorage.setItem(FONT_KEY, String(v))
      } catch (e) {
        /* private mode */
      }
    }
  },
  async mounted() {
    this.beforeUnload = (e) => {
      if (!this.connectedTabs.length) return undefined
      e.preventDefault()
      e.returnValue = ''
      return ''
    }
    window.addEventListener('beforeunload', this.beforeUnload)

    this.loading = true
    try {
      await this.loadServers()
      const byId = new Map(this.servers.map((s) => [s.id, s]))
      const savedIds = readSavedTabs()
        .map((t) => Number(t.id))
        .filter((id) => id && !Number.isNaN(id))
      // The list endpoint is capped at 200 rows; fetch saved tabs outside that
      // page individually instead of silently dropping them.
      const missing = savedIds.filter((id) => !byId.has(id))
      if (missing.length) {
        const fetched = await Promise.all(
          missing.map((id) => api.servers.get(id).catch(() => null))
        )
        fetched.forEach((srv) => {
          if (!srv) return
          byId.set(srv.id, srv)
          this.servers.push(srv)
        })
      }
      const restored = savedIds.map((id) => byId.get(id)).filter(Boolean)
      this.tabs = restored.map((s) => ({ id: s.id, server: s }))
      this.tabs.forEach((t) => this.$set(this.paneStates, t.id, 'idle'))

      const routeId = Number(this.$route.params.id)
      if (routeId && !this.tabs.some((t) => t.id === routeId)) {
        await this.addTab(routeId)
      } else if (routeId) {
        this.activate(routeId, true)
      }
      if (!this.activeId && this.tabs.length) this.activate(this.tabs[0].id, true)
      this.saveTabs()
    } catch (e) {
      /* loadServers already reported the failure; the empty state remains */
    } finally {
      this.loading = false
    }
  },
  beforeRouteLeave(to, from, next) {
    if (!this.connectedTabs.length) {
      next()
      return
    }
    const names = this.connectedTabs.map((t) => this.tabLabel(t)).join('、')
    const ok = window.confirm(`终端「${names}」仍在连接，离开页面会终止远端 Shell。确定离开？`)
    next(ok)
  },
  beforeDestroy() {
    window.removeEventListener('beforeunload', this.beforeUnload)
  },
  methods: {
    formatTime,
    statusClass,
    statusText,

    tabLabel(tab) {
      const s = tab.server || {}
      return s.name || s.hostname || s.ip || `#${tab.id}`
    },
    serverLabel(s) {
      const st = s.status ? ` · ${statusText(s.status)}` : ''
      return `${s.name || s.hostname || s.ip} (${s.ip})${st}`
    },
    dotClass(id) {
      const st = this.paneStates[id]
      if (st === 'open') return 'open'
      if (st === 'connecting') return 'connecting'
      if (st === 'error' || st === 'closed') return 'error'
      return 'idle'
    },
    onPaneState(id, state) {
      // A pane keeps emitting while its tab is being closed (the socket closes
      // asynchronously); ignore states for tabs that no longer exist.
      if (!this.tabs.some((t) => t.id === id)) return
      this.$set(this.paneStates, id, state)
    },
    saveTabs() {
      try {
        const slim = this.tabs.map((t) => ({ id: t.id, name: this.tabLabel(t), ip: t.server.ip }))
        sessionStorage.setItem(TABS_KEY, JSON.stringify(slim))
      } catch (e) {
        /* private mode */
      }
    },

    async loadServers() {
      try {
        const res = await api.servers.list({ page: 1, page_size: 200 })
        this.servers = res.items || res || []
      } catch (e) {
        this.$message.error(e.message || '加载服务器失败')
        throw e
      }
    },

    async ensureTab(id) {
      if (!id || Number.isNaN(id)) return
      if (this.tabs.some((t) => t.id === id)) {
        this.activate(id)
        return
      }
      await this.addTab(id)
    },

    async addTab(id) {
      let srv = this.servers.find((s) => s.id === id)
      if (!srv) {
        try {
          srv = await api.servers.get(id)
          this.servers.push(srv)
        } catch (e) {
          this.$message.error(e.message || '服务器不存在或无权访问')
          if (!this.tabs.length) this.$router.replace('/terminal')
          return false
        }
      }
      this.tabs.push({ id: srv.id, server: srv })
      this.$set(this.paneStates, srv.id, 'idle')
      this.activate(srv.id)
      this.saveTabs()
      return true
    },

    activate(id, silent) {
      this.activeId = id
      if (Number(this.$route.params.id) !== id) {
        const nav = this.$router.replace({ name: 'terminal', params: { id: String(id) } })
        if (nav && nav.catch) nav.catch(() => {})
      }
      if (!silent) this.$nextTick(() => this.focusActive())
    },

    focusActive() {
      const pane = this.activePane()
      if (pane) pane.focusPane()
    },
    paneList() {
      const r = this.$refs.panes
      if (!r) return []
      return Array.isArray(r) ? r : [r]
    },
    activePane() {
      const idx = this.tabs.findIndex((t) => t.id === this.activeId)
      if (idx < 0) return null
      return this.paneList()[idx] || null
    },

    closeTab(id) {
      const tab = this.tabs.find((t) => t.id === id)
      if (!tab) return
      const connected = ['open', 'connecting'].includes(this.paneStates[id])
      if (!connected) {
        this.doCloseTab(id)
        return
      }
      this.$confirm(
        `终端「${this.tabLabel(tab)}」仍在连接，关闭会终止远端 Shell。确定关闭？`,
        '关闭终端',
        { type: 'warning' }
      )
        .then(() => this.doCloseTab(id))
        .catch(() => {})
    },
    doCloseTab(id) {
      const i = this.tabs.findIndex((t) => t.id === id)
      if (i < 0) return
      this.tabs.splice(i, 1)
      this.$delete(this.paneStates, id)
      this.saveTabs()
      if (this.activeId !== id) return
      if (this.tabs.length) {
        this.activate(this.tabs[Math.min(i, this.tabs.length - 1)].id)
      } else {
        this.activeId = null
        this.$router.replace('/terminal')
      }
    },

    openPicker() {
      this.pickerId = null
      this.pickerVisible = true
      this.loadServers().catch(() => {})
    },
    // Same server-side IP/name/hostname search as the File page.
    async searchPickerServers(query) {
      const q = (query || '').trim()
      if (!q) {
        this.loadServers().catch(() => {})
        return
      }
      this.pickerSearching = true
      try {
        const res = await api.servers.list({ page: 1, page_size: 50, q })
        const items = res.items || []
        const selected = this.servers.find((s) => s.id === this.pickerId)
        if (selected && !items.some((s) => s.id === selected.id)) items.unshift(selected)
        this.servers = items
      } catch (e) {
        this.$message.error(e.message || '搜索服务器失败')
      } finally {
        this.pickerSearching = false
      }
    },
    async openPicked() {
      if (!this.pickerId) return
      const ok = await this.addTab(this.pickerId)
      if (ok) this.pickerVisible = false
    },

    async refreshServers() {
      this.refreshing = true
      try {
        await this.loadServers()
        this.tabs.forEach((t) => {
          const srv = this.servers.find((s) => s.id === t.id)
          if (srv) this.$set(t, 'server', srv)
        })
        this.$message.success('状态已刷新')
      } catch (e) {
        /* loadServers already reported it */
      } finally {
        this.refreshing = false
      }
    },

    toggleFullscreen() {
      this.fullscreen = !this.fullscreen
      this.$nextTick(() => {
        this.paneList().forEach((p) => p.fitSoon())
        if (this.fullscreen) this.focusActive()
      })
    },

    goExec() {
      if (this.activeTab) this.$router.push(`/servers/${this.activeTab.id}/execute`)
    },
    goFiles() {
      if (this.activeTab) {
        this.$router.push({ path: '/files', query: { server: this.activeTab.id, path: '/' } })
      }
    },
    goHistory() {
      if (this.activeTab) {
        this.$router.push({ path: '/history', query: { server_id: this.activeTab.id } })
      }
    }
  }
}
</script>

<style scoped>
.workspace-panel {
  display: flex;
  flex-direction: column;
}

.ws-tabs {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}
.ws-tabs .spacer {
  flex: 1;
}

/* 机架标签：方形单元，激活时底部一条铜色导轨 */
.ws-tab {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  max-width: 260px;
  padding: 4px 8px 4px 9px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: var(--panel-2);
  color: var(--muted);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
  transition: color 120ms ease-out, border-color 120ms ease-out, background 120ms ease-out;
}
.ws-tab:hover {
  color: var(--text);
  border-color: var(--muted);
}
.ws-tab.active {
  color: var(--text-strong);
  border-color: var(--border);
  background: var(--panel);
  box-shadow: inset 0 -2px 0 var(--accent);
}
.ws-tab-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ws-tab-ip {
  font-family: var(--font-mono);
  font-size: 10.5px;
  opacity: 0.75;
}
.ws-tab-close {
  font-size: 12px;
  opacity: 0.55;
}
.ws-tab-close:hover {
  opacity: 1;
  color: var(--danger);
}

.ws-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--muted);
  flex: 0 0 auto;
}
.ws-dot.open {
  background: var(--success);
  box-shadow: 0 0 6px var(--success);
}
.ws-dot.connecting {
  background: var(--warn);
}
.ws-dot.error {
  background: var(--danger);
}

.ws-font {
  width: 88px;
}

.ws-meta {
  display: flex;
  align-items: center;
  gap: 13px;
  flex-wrap: wrap;
  margin-bottom: 10px;
  font-size: 11.5px;
  color: var(--muted);
}
.ws-meta strong {
  color: var(--text-strong);
  font-size: 13px;
  font-weight: 600;
}
.ws-meta .spacer {
  flex: 1;
}

.ws-body {
  position: relative;
  display: flex;
  height: calc(100vh - 320px);
  min-height: 360px;
}

.ws-empty {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--muted);
  border: 1px dashed var(--border);
  border-radius: var(--radius);
  background: var(--bg-soft);
}
.ws-empty i {
  font-size: 26px;
  color: var(--accent);
}
.ws-empty p {
  margin: 0;
  font-size: 13px;
}

.picker-tip {
  margin-top: 10px;
  font-size: 11.5px;
}

/* Fullscreen workspace: the tab bar and meta stay visible, the active pane
 * takes all remaining height. */
.terminal-workspace.is-fullscreen .workspace-panel {
  position: fixed;
  inset: 0;
  z-index: 2000;
  margin: 0;
  border: 0;
  border-radius: 0;
  padding: 10px 14px 14px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.terminal-workspace.is-fullscreen .ws-body {
  flex: 1 1 auto;
  height: auto;
  min-height: 0;
}

@media (max-width: 760px) {
  .ws-body {
    height: 60vh;
  }
  .ws-tab-ip {
    display: none;
  }
}
</style>
