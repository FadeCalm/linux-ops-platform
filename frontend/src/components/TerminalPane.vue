<template>
  <div v-show="active" class="term-pane">
    <div class="pane-toolbar">
      <span class="conn-badge" :class="'conn-' + state">{{ connLabel }}</span>
      <span v-if="server.ip" class="muted mono">{{ agentAddr }}</span>
      <span class="muted">会话 {{ elapsedLabel }}</span>
      <span class="muted">尺寸 {{ cols }} × {{ rows }}</span>
      <span v-if="shellHint" class="muted">{{ shellHint }}</span>
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
  </div>
</template>

<script>
import { Terminal } from 'xterm'
import { FitAddon } from 'xterm-addon-fit'
import 'xterm/css/xterm.css'
import { wsUrl } from '@/api/base'

// The console follows the UI theme: a graphite readout in the dark "rack"
// theme and a paper-like readout in the light "anodized" theme. Copper is the
// only warm color, used for the cursor and selection.
const TERMINAL_THEMES = {
  dark: {
    background: '#0b0f11',
    foreground: '#d6dee3',
    cursor: '#c07a3e',
    cursorAccent: '#0b0f11',
    selectionBackground: 'rgba(192, 122, 62, 0.3)',
    black: '#141a1d',
    red: '#e26860',
    green: '#5fbf85',
    yellow: '#d9ad5a',
    blue: '#7fa6c9',
    magenta: '#b48ead',
    cyan: '#74b7b0',
    white: '#c7d0d5',
    brightBlack: '#5d6a70',
    brightRed: '#f08a82',
    brightGreen: '#7fd6a2',
    brightYellow: '#ecc77d',
    brightBlue: '#9cc0dd',
    brightMagenta: '#cba8c4',
    brightCyan: '#93d0c9',
    brightWhite: '#eef3f5'
  },
  light: {
    background: '#fbfbf9',
    foreground: '#1c2428',
    cursor: '#9a5b2a',
    cursorAccent: '#fbfbf9',
    selectionBackground: 'rgba(154, 91, 42, 0.22)',
    black: '#000000',
    red: '#b02a23',
    green: '#157a47',
    yellow: '#8a5b07',
    blue: '#225f91',
    magenta: '#7c3f73',
    cyan: '#0f6f6a',
    white: '#55636a',
    brightBlack: '#6b777d',
    brightRed: '#c94a42',
    brightGreen: '#1f9a5c',
    brightYellow: '#a87410',
    brightBlue: '#3379ad',
    brightMagenta: '#965a8c',
    brightCyan: '#188f88',
    brightWhite: '#2c383e'
  }
}

function currentThemeMode() {
  return document.documentElement.getAttribute('data-theme') === 'light' ? 'light' : 'dark'
}

// TerminalPane owns one PTY session. The workspace keeps every tab mounted
// (v-show), so switching tabs does not drop the SSH-like session; it only
// hides it and re-fits on activation.
export default {
  name: 'TerminalPane',
  props: {
    server: { type: Object, required: true },
    active: { type: Boolean, default: false },
    fontSize: { type: Number, default: 12 }
  },
  data() {
    return {
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
      return Number(this.server && this.server.id)
    },
    agentAddr() {
      if (!this.server || !this.server.ip) return '-'
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
    active(v) {
      if (!v) return
      this.$nextTick(() => {
        this.fitRetries = 0
        this.fitSoon()
        if (this.state === 'open' && this.term) this.term.focus()
      })
    },
    fontSize(v) {
      if (!this.term) return
      this.term.options.fontSize = v
      this.$nextTick(() => this.doFit())
    },
    state(v) {
      this.$emit('state', v)
    }
  },
  mounted() {
    this.initTerminal()
    // Requirement: opening a terminal from Resource connects immediately; the
    // toolbar button is only needed after an explicit disconnect.
    this.$nextTick(() => {
      this.doFit()
      if (this.serverId) this.connect()
    })
    if (typeof document !== 'undefined' && document.fonts && document.fonts.ready) {
      // The bundled web font may finish loading after xterm measured its cell
      // size; re-measure so the grid matches the font that is actually drawn.
      document.fonts.ready.then(() => {
        this.remeasure()
        this.doFit()
      })
    }
    if (typeof MutationObserver !== 'undefined') {
      this.themeObserver = new MutationObserver(() => this.applyTermTheme())
      this.themeObserver.observe(document.documentElement, {
        attributes: true,
        attributeFilter: ['data-theme']
      })
    }
    // A pane mounted while hidden cannot measure its cells; fit again the
    // moment the browser reports it visible.
    if (typeof IntersectionObserver !== 'undefined') {
      this.intersectionObserver = new IntersectionObserver((entries) => {
        if (!entries.some((e) => e.isIntersecting)) return
        this.fitRetries = 0
        this.fitSoon()
      })
      this.intersectionObserver.observe(this.$refs.host)
    }
  },
  beforeDestroy() {
    if (this.themeObserver) {
      this.themeObserver.disconnect()
      this.themeObserver = null
    }
    if (this.intersectionObserver) {
      this.intersectionObserver.disconnect()
      this.intersectionObserver = null
    }
    if (this.fitRetryTimer) {
      clearTimeout(this.fitRetryTimer)
      this.fitRetryTimer = null
    }
    this.teardown()
  },
  methods: {
    initTerminal() {
      this.term = new Terminal({
        cursorBlink: true,
        fontSize: this.fontSize,
        lineHeight: 1.25,
        fontFamily: '"IBM Plex Mono", "JetBrains Mono", "Cascadia Mono", Consolas, "DejaVu Sans Mono", monospace',
        scrollback: 5000,
        theme: TERMINAL_THEMES[currentThemeMode()]
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
    applyTermTheme() {
      if (!this.term) return
      this.term.options.theme = TERMINAL_THEMES[currentThemeMode()]
    },
    doFit() {
      if (!this.fitAddon || !this.$refs.host) return
      const el = this.$refs.host
      if (!el.clientWidth || !el.clientHeight) return
      let dims = null
      try {
        dims = this.fitAddon.proposeDimensions()
      } catch (e) {
        dims = null
      }
      // xterm reports a zero cell size until it has measured itself after the
      // pane becomes visible. fit() silently gives up in that state, leaving
      // the terminal stuck at 80x24, so retry briefly instead of losing it.
      if (!dims || isNaN(dims.cols) || isNaN(dims.rows)) {
        this.scheduleFitRetry()
        return
      }
      this.fitRetries = 0
      try {
        this.fitAddon.fit()
      } catch (e) {
        /* xterm can throw while the element is hidden; ignore */
      }
    },
    scheduleFitRetry() {
      if (this.fitRetries >= 10 || this.fitRetryTimer) return
      this.fitRetries = (this.fitRetries || 0) + 1
      this.fitRetryTimer = setTimeout(() => {
        this.fitRetryTimer = null
        this.doFit()
      }, 100)
    },
    remeasure() {
      // Font options are the only public trigger for xterm to re-measure its
      // character cell; bounce the value and restore it in the same frame.
      if (!this.term || !this.term.element) return
      const size = this.term.options.fontSize
      this.term.options.fontSize = size + 1
      this.term.options.fontSize = size
    },
    fitSoon() {
      this.doFit()
      // xterm re-measures cell size when the element becomes visible; fit once
      // more on the next frame so cols/rows use the fresh measurement.
      if (typeof requestAnimationFrame === 'function') {
        requestAnimationFrame(() => this.doFit())
      }
    },
    focusPane() {
      this.fitSoon()
      if (this.term) this.term.focus()
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
        // The container may have been resized while the handshake was in
        // flight; fit once more before reporting the size to the PTY.
        this.doFit()
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
    }
  }
}
</script>

<style scoped>
.term-pane {
  display: flex;
  flex: 1 1 0;
  flex-direction: column;
  height: 100%;
  width: 100%;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}

.pane-toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
  margin-bottom: 10px;
}
.pane-toolbar .spacer {
  flex: 1;
}

.conn-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 7px;
  border-radius: var(--radius-sm);
  font-size: 11px;
  font-weight: 600;
  border: 1px solid transparent;
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
}
.conn-connecting {
  color: var(--warn);
  background: var(--warn-soft);
}
.conn-error,
.conn-closed {
  color: var(--danger);
  background: var(--danger-soft);
}

/* The console follows the UI theme (see TERMINAL_THEMES). The workspace
 * controls the height; the host fills whatever space is left so long input
 * lines get the full width. */
.xterm-host {
  flex: 1 1 auto;
  min-height: 0;
  background: var(--term-bg);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 8px 10px;
  overflow: hidden;
}
.xterm-host :deep(.xterm) {
  height: 100%;
}
.xterm-host :deep(.xterm-viewport) {
  background: transparent !important;
}
</style>
