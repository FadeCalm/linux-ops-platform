<template>
  <div>
    <!-- server summary -->
    <div v-loading="loadingServer" class="panel server-hero">
      <div class="hero-left">
        <el-button class="back-btn" size="mini" icon="el-icon-back" @click="goBack">返回服务器列表</el-button>
        <div class="hero-id">
          <div class="hero-name">
            <strong>{{ server.name || '加载中…' }}</strong>
            <span v-if="server.id" class="status-badge" :class="statusClass(server.status)">
              {{ statusText(server.status) }}
            </span>
          </div>
          <div class="hero-meta">
            <span class="mono">{{ agentAddr }}</span>
            <span class="muted">{{ server.hostname || '-' }}</span>
            <span class="muted">{{ server.os || '-' }} / {{ server.arch || '-' }}</span>
            <span class="muted mono">agent {{ server.agent_version || '-' }}</span>
            <span class="muted">心跳 {{ formatTime(server.last_heartbeat_at) }}</span>
          </div>
        </div>
      </div>
      <div class="hero-actions">
        <el-button size="mini" icon="el-icon-refresh" :loading="checking" @click="refreshServer">刷新状态</el-button>
        <el-button size="mini" icon="el-icon-cpu" @click="goExec">单机执行</el-button>
        <el-button size="mini" icon="el-icon-folder-opened" @click="goFiles">文件管理</el-button>
        <el-button size="mini" icon="el-icon-document" @click="goHistory">任务历史</el-button>
      </div>
    </div>

    <!-- interactive terminal -->
    <div class="panel">
      <div class="toolbar">
        <span class="conn-badge" :class="'conn-' + state">{{ connLabel }}</span>
        <span class="muted mono" v-if="server.ip">{{ server.ip }}:{{ server.agent_port || 19090 }}</span>
        <span class="muted">会话时长 {{ elapsedLabel }}</span>
        <span class="muted">终端尺寸 {{ cols }} × {{ rows }}</span>
        <span class="muted" v-if="shellHint">{{ shellHint }}</span>
        <div class="spacer"></div>
        <el-button size="mini" icon="el-icon-delete" @click="clearScreen">清屏</el-button>
        <el-button size="mini" icon="el-icon-document-copy" @click="copySelection">复制选区</el-button>
        <el-button
          v-if="!connected"
          size="mini"
          type="primary"
          icon="el-icon-connection"
          :loading="state === 'connecting'"
          @click="connect"
        >连接</el-button>
        <el-button
          v-else
          size="mini"
          type="danger"
          icon="el-icon-switch-button"
          @click="disconnect(true)"
        >断开</el-button>
      </div>

      <div ref="host" class="xterm-host"></div>

      <div class="term-foot">
        <span>
          <i class="el-icon-warning-outline"></i>
          交互式 PTY 会话与「命令执行」的普通任务是两套引擎：这里的命令不会生成平台任务记录，
          平台只审计会话的打开/关闭（operator = anonymous）。
        </span>
        <span>
          断开连接、刷新或关闭页面都会终止远端 Shell。空闲超时与单次会话最长时长由目标主机
          Agent 的 <code>terminal.idle_timeout_sec</code> / <code>terminal.max_duration_sec</code>
          决定（样例配置为 600s / 3600s，0 表示关闭）。
        </span>
      </div>
    </div>
  </div>
</template>

<script>
import { Terminal } from 'xterm'
import { FitAddon } from 'xterm-addon-fit'
import 'xterm/css/xterm.css'
import api from '@/api'
import { wsUrl } from '@/api/base'
import { formatTime, statusClass, statusText } from '@/utils/format'

const TERMINAL_THEME = {
  background: '#ffffff',
  foreground: '#1f2329',
  cursor: '#111111',
  cursorAccent: '#ffffff',
  selectionBackground: 'rgba(9, 105, 218, 0.24)',
  black: '#000000',
  red: '#cd3131',
  green: '#00bc00',
  yellow: '#949800',
  blue: '#0451a5',
  magenta: '#bc05bc',
  cyan: '#0598bc',
  white: '#555555',
  brightBlack: '#666666',
  brightRed: '#cd3131',
  brightGreen: '#14ce14',
  brightYellow: '#b5ba00',
  brightBlue: '#0451a5',
  brightMagenta: '#bc05bc',
  brightCyan: '#0598bc',
  brightWhite: '#a5a5a5'
}

export default {
  name: 'TerminalView',
  data() {
    return {
      loadingServer: false,
      checking: false,
      server: {},
      // idle | connecting | open | closed | error
      state: 'idle',
      cols: 80,
      rows: 24,
      elapsedSec: 0,
      lastMessage: ''
    }
  },
  computed: {
    serverId() {
      return Number(this.$route.params.id)
    },
    agentAddr() {
      if (!this.server.ip) return '-'
      return `${this.server.ip}:${this.server.agent_port || 19090}`
    },
    connected() {
      return this.state === 'open' || this.state === 'connecting'
    },
    connLabel() {
      return {
        idle: '未连接',
        connecting: '连接中',
        open: '已连接',
        closed: '已断开',
        error: '连接失败'
      }[this.state]
    },
    elapsedLabel() {
      const s = this.elapsedSec
      const m = Math.floor(s / 60)
      const r = s % 60
      return m > 0 ? `${m}m ${String(r).padStart(2, '0')}s` : `${r}s`
    },
    shellHint() {
      return this.state === 'error' && this.lastMessage ? this.lastMessage : ''
    }
  },
  watch: {
    '$route.params.id'(next, prev) {
      if (next && next !== prev) this.bootstrap()
    }
  },
  mounted() {
    this.initTerminal()
    this.bootstrap()
    this.beforeUnload = (e) => {
      if (!this.connected) return undefined
      e.preventDefault()
      e.returnValue = ''
      return ''
    }
    window.addEventListener('beforeunload', this.beforeUnload)
  },
  beforeRouteLeave(to, from, next) {
    if (!this.connected) {
      next()
      return
    }
    const ok = window.confirm('当前交互式会话仍在连接，离开页面会终止远端 Shell。确定离开？')
    next(ok)
  },
  beforeDestroy() {
    window.removeEventListener('beforeunload', this.beforeUnload)
    this.teardown()
  },
  methods: {
    formatTime,
    statusClass,
    statusText,

    async bootstrap() {
      this.resetTerminal()
      await this.loadServer()
    },
    resetTerminal() {
      this.disconnect(false)
      if (this.term) {
        this.term.reset()
        this.term.writeln('就绪。点击「连接」在目标主机上打开交互式 Shell（PTY）。')
      }
      this.elapsedSec = 0
      this.lastMessage = ''
      this.state = 'idle'
    },
    initTerminal() {
      this.term = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        lineHeight: 1.25,
        fontFamily: '"JetBrains Mono", "Fira Code", Consolas, "DejaVu Sans Mono", monospace',
        scrollback: 5000,
        theme: TERMINAL_THEME
      })
      this.fitAddon = new FitAddon()
      this.term.loadAddon(this.fitAddon)
      this.term.open(this.$refs.host)
      this.doFit()

      // Keystrokes go to the agent as *binary* frames; the agent treats text
      // frames as control messages (resize).
      this.term.onData((data) => this.sendInput(data))
      this.term.onResize(({ cols, rows }) => {
        this.cols = cols
        this.rows = rows
        this.sendResize()
      })

      if (typeof ResizeObserver !== 'undefined') {
        this.resizeObserver = new ResizeObserver(() => this.doFit())
        this.resizeObserver.observe(this.$refs.host)
      }
      window.addEventListener('resize', this.doFit)
    },
    doFit() {
      if (!this.fitAddon || !this.$refs.host) return
      const el = this.$refs.host
      if (!el.clientWidth || !el.clientHeight) return
      try {
        this.fitAddon.fit()
      } catch (e) {
        /* xterm can throw while the element is hidden; ignore */
      }
    },
    teardown() {
      this.disconnect(false)
      this.stopTimer()
      window.removeEventListener('resize', this.doFit)
      if (this.resizeObserver) {
        this.resizeObserver.disconnect()
        this.resizeObserver = null
      }
      if (this.term) {
        this.term.dispose()
        this.term = null
      }
    },

    async loadServer() {
      if (!this.serverId || Number.isNaN(this.serverId)) {
        this.$message.error('无效的服务器 ID')
        this.$router.replace('/servers')
        return
      }
      this.loadingServer = true
      try {
        this.server = await api.servers.get(this.serverId)
      } catch (e) {
        this.$message.error(e.message || '服务器不存在')
        this.$router.replace('/servers')
      } finally {
        this.loadingServer = false
      }
    },
    // Re-read the server record (kept fresh by the backend heartbeat). Health
    // checks are now a batch action on the server list.
    async refreshServer() {
      if (!this.serverId) return
      this.checking = true
      try {
        this.server = await api.servers.get(this.serverId)
        this.$message.success('状态已刷新')
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.checking = false
      }
    },

    connect() {
      if (!this.serverId || this.connected) return
      if (this.server.status && this.server.status !== 'online') {
        this.term.writeln('\x1b[33m[提示] 该主机状态不是 online，连接仍会尝试；失败请先检查 Agent。\x1b[0m')
      }
      this.state = 'connecting'
      this.lastMessage = ''
      this.term.writeln('\x1b[90m正在连接 ' + this.agentAddr + ' …\x1b[0m')

      let ws
      try {
        ws = new WebSocket(wsUrl(`/ws/terminal/${this.serverId}`))
      } catch (e) {
        this.state = 'error'
        this.lastMessage = e.message
        return
      }
      ws.binaryType = 'arraybuffer'
      this.ws = ws
      this.closing = false

      ws.onopen = () => {
        this.state = 'open'
        this.resetTimer()
        this.startTimer()
        this.sendResize()
        this.term.focus()
      }
      ws.onmessage = (ev) => this.onFrame(ev)
      ws.onerror = () => {
        this.state = 'error'
        this.lastMessage = this.lastMessage || 'WebSocket 连接错误'
      }
      ws.onclose = () => {
        this.stopTimer()
        this.ws = null
        if (this.closing) {
          this.state = 'idle'
          this.closing = false
          return
        }
        if (this.state !== 'error') this.state = 'closed'
        this.term.writeln('\r\n\x1b[90m[连接已关闭] 远端 Shell 已终止，如需继续请重新连接。\x1b[0m')
      }
    },
    disconnect(announce) {
      if (this.ws) {
        this.closing = true
        try {
          this.ws.close()
        } catch (e) {
          /* already closed */
        }
        this.ws = null
      }
      this.stopTimer()
      if (announce) {
        this.state = 'idle'
        if (this.term) this.term.writeln('\r\n\x1b[90m[已断开]\x1b[0m')
      }
    },
    onFrame(ev) {
      if (typeof ev.data === 'string') {
        let msg
        try {
          msg = JSON.parse(ev.data)
        } catch (e) {
          return
        }
        this.handleControl(msg)
        return
      }
      if (ev.data instanceof ArrayBuffer && this.term) {
        this.term.write(new Uint8Array(ev.data))
      }
    },
    handleControl(msg) {
      switch (msg.type) {
        case 'error':
          this.state = 'error'
          this.lastMessage = msg.message || '连接失败'
          this.term.writeln(`\r\n\x1b[31m[连接失败] ${this.lastMessage}\x1b[0m`)
          break
        case 'notice':
          this.term.writeln(`\r\n\x1b[33m[提示] ${msg.message}\x1b[0m`)
          break
        case 'exit':
          this.term.writeln(
            `\r\n\x1b[90m[会话结束] 退出码 ${msg.code === undefined ? '-' : msg.code}\x1b[0m`
          )
          break
        default:
          break
      }
    },
    sendInput(data) {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
      this.ws.send(new TextEncoder().encode(data))
    },
    sendResize() {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
      try {
        this.ws.send(JSON.stringify({ type: 'resize', cols: this.cols, rows: this.rows }))
      } catch (e) {
        /* ignore */
      }
    },
    resetTimer() {
      this.elapsedSec = 0
      this.startedAt = Date.now()
    },
    startTimer() {
      this.stopTimer()
      this.timer = setInterval(() => {
        this.elapsedSec = Math.floor((Date.now() - this.startedAt) / 1000)
      }, 1000)
    },
    stopTimer() {
      if (this.timer) {
        clearInterval(this.timer)
        this.timer = null
      }
    },
    clearScreen() {
      if (this.term) {
        this.term.clear()
        this.term.focus()
      }
    },
    async copySelection() {
      if (!this.term) return
      const text = this.term.getSelection()
      if (!text) {
        this.$message.info('请先用鼠标选中要复制的终端内容')
        return
      }
      try {
        await navigator.clipboard.writeText(text)
        this.$message.success('已复制选区')
      } catch (e) {
        this.$message.error('复制失败：' + e.message)
      }
    },

    goBack() {
      this.$router.push('/servers')
    },
    goExec() {
      this.$router.push(`/servers/${this.serverId}/execute`)
    },
    goFiles() {
      this.$router.push({ path: '/files', query: { server: this.serverId, path: '/' } })
    },
    goHistory() {
      this.$router.push({ path: '/history', query: { server_id: this.serverId } })
    }
  }
}
</script>

<style scoped>
.server-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  flex-wrap: wrap;
}
.hero-left {
  display: flex;
  align-items: center;
  gap: 18px;
  min-width: 0;
}
.hero-id {
  min-width: 0;
}
.hero-name {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 18px;
  color: var(--text-strong);
}
.hero-meta {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
  margin-top: 6px;
  font-size: 12px;
  color: var(--muted);
}
.hero-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.conn-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
  border: 1px solid var(--border);
  color: var(--muted);
  background: var(--muted-soft);
}
.conn-badge::before {
  content: '';
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.conn-open {
  color: var(--success);
  background: var(--success-soft);
  border-color: transparent;
}
.conn-connecting {
  color: var(--warn);
  background: var(--warn-soft);
  border-color: transparent;
}
.conn-error,
.conn-closed {
  color: var(--danger);
  background: var(--danger-soft);
  border-color: transparent;
}

/* The terminal keeps its own light palette in both UI themes, independent of
 * the surrounding panel theme. */
.xterm-host {
  background: #ffffff;
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 8px 10px;
  height: calc(100vh - 380px);
  min-height: 420px;
  overflow: hidden;
}
.xterm-host :deep(.xterm) {
  height: 100%;
}
.xterm-host :deep(.xterm-viewport) {
  background: transparent !important;
}

.term-foot {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 10px;
  font-size: 11.5px;
  line-height: 1.6;
  color: var(--muted);
}

@media (max-width: 760px) {
  .xterm-host {
    height: 60vh;
  }
}
</style>
