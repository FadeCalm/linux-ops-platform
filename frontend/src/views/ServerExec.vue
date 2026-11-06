<template>
  <div>
    <!-- server summary -->
    <div v-loading="loadingServer" class="panel server-hero">
      <div class="hero-left">
        <el-button class="back-btn" size="mini" icon="el-icon-back" @click="goBack">返回批量执行</el-button>
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
        <el-button size="mini" icon="el-icon-connection" @click="goTerminal">交互终端</el-button>
        <el-button size="mini" icon="el-icon-folder-opened" @click="goFiles">文件管理</el-button>
        <el-button size="mini" icon="el-icon-document" @click="goHistory">任务历史</el-button>
      </div>
    </div>

    <!-- command form -->
    <div class="panel">
      <h3 class="panel-title"><span class="accent">●</span> 执行命令</h3>
      <el-form label-width="86px">
        <el-form-item label="命令">
          <el-input
            v-model="form.command"
            type="textarea"
            :rows="7"
            placeholder="例如：df -h&#10;支持多行脚本、管道、环境变量"
            @keydown.ctrl.enter.native="run"
          />
        </el-form-item>
        <div class="quick-row">
          <span class="muted">快捷命令</span>
          <el-button v-for="q in quickCommands" :key="q.label" size="mini" @click="form.command = q.cmd">
            {{ q.label }}
          </el-button>
          <span class="muted quick-hint">Ctrl + Enter 执行</span>
        </div>
        <el-form-item label="工作目录">
          <el-input v-model="form.work_dir" placeholder="留空使用 Agent 默认目录" style="max-width: 360px" />
        </el-form-item>
        <el-form-item label="超时(秒)">
          <el-input-number v-model="form.timeout_sec" :min="1" :max="3600" />
        </el-form-item>
      </el-form>
      <div class="run-row">
        <span class="muted">
          仅在此主机执行 · 在线状态：{{ statusText(server.status) }}
          <template v-if="server.status !== 'online'">（离线时任务会失败）</template>
        </span>
        <el-button type="primary" icon="el-icon-video-play" :loading="submitting" @click="run">
          执行
        </el-button>
      </div>
    </div>

    <!-- output -->
    <div v-if="target" class="panel">
      <div class="toolbar">
        <h3 class="panel-title" style="margin: 0"><span class="accent">●</span> 输出</h3>
        <span class="status-badge" :class="statusClass(target.status)">{{ statusText(target.status) }}</span>
        <span class="muted mono">任务 {{ shortId(task && task.id) }}</span>
        <span class="muted">连接：{{ wsLabel }}</span>
        <div class="spacer"></div>
        <el-button size="mini" icon="el-icon-refresh" @click="reconnectNow">重连</el-button>
        <el-button
          size="mini"
          type="warning"
          icon="el-icon-circle-close"
          :disabled="isTerminal(target.status)"
          @click="cancelTarget"
        >取消</el-button>
        <el-button size="mini" :disabled="!isTerminal(target.status)" @click="retryTarget">重试</el-button>
      </div>

      <div class="term-toolbar">
        <span class="term-meta">主机：<span class="mono">{{ agentAddr }}</span></span>
        <span class="term-meta">
          退出码：<span class="mono">{{ target.exit_code === null || target.exit_code === undefined ? '-' : target.exit_code }}</span>
        </span>
        <span class="term-meta">耗时：{{ formatDuration(target.duration_ms) }}</span>
        <span class="term-meta" v-if="task">开始：{{ formatTime(task.created_at) }}</span>
        <span v-if="target.error" class="term-meta" style="color: var(--danger)">{{ target.error }}</span>
        <div class="spacer"></div>
        <el-button size="mini" type="text" tag="a" :href="downloadUrl('stdout')" target="_blank">下载 stdout</el-button>
        <el-button size="mini" type="text" tag="a" :href="downloadUrl('stderr')" target="_blank">下载 stderr</el-button>
      </div>

      <div class="term-label">stdout</div>
      <div :id="'out-' + target.id" class="term">{{ target.stdout || '（等待输出…）' }}</div>
      <template v-if="target.stderr">
        <div class="term-label" style="color: var(--danger)">stderr</div>
        <div :id="'err-' + target.id" class="term err-term">{{ target.stderr }}</div>
      </template>
    </div>

    <!-- recent tasks -->
    <div class="panel">
      <div class="toolbar">
        <h3 class="panel-title" style="margin: 0"><span class="accent">●</span> 最近任务</h3>
        <div class="spacer"></div>
        <el-button size="mini" icon="el-icon-refresh" :loading="loadingHistory" @click="loadHistory">刷新</el-button>
      </div>
      <el-table v-loading="loadingHistory" :data="history" size="mini" border empty-text="该主机还没有执行记录">
        <el-table-column label="任务" width="100">
          <template slot-scope="{ row }"><span class="mono">{{ shortId(row.id) }}</span></template>
        </el-table-column>
        <el-table-column prop="command" label="命令" min-width="280" show-overflow-tooltip>
          <template slot-scope="{ row }"><span class="mono">{{ row.command }}</span></template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template slot-scope="{ row }">
            <span class="status-badge" :class="statusClass(row.status)">{{ statusText(row.status) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template slot-scope="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="90">
          <template slot-scope="{ row }">
            <el-button type="text" @click="loadTask(row.id)">查看</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>
  </div>
</template>

<script>
import api from '@/api'
import { wsUrl } from '@/api/base'
import {
  formatTime,
  formatDuration,
  shortId,
  statusClass,
  statusText,
  isTerminal,
  decodeBase64
} from '@/utils/format'

export default {
  name: 'ServerExec',
  data() {
    return {
      loadingServer: false,
      checking: false,
      server: {},
      limits: {},
      form: { command: '', work_dir: '', timeout_sec: 300 },
      submitting: false,
      task: null,
      target: null,
      history: [],
      loadingHistory: false,
      ws: null,
      wsState: 'idle',
      reconnectTimer: null,
      pingTimer: null,
      manualClose: false,
      lastSeq: {},
      quickCommands: [
        { label: '系统信息', cmd: 'uname -a; cat /etc/os-release 2>/dev/null | head -5' },
        { label: '磁盘', cmd: 'df -h' },
        { label: '内存', cmd: 'free -m' },
        { label: '负载', cmd: 'uptime' },
        { label: 'CPU 占用 TOP', cmd: "ps -eo pid,ppid,user,%cpu,%mem,cmd --sort=-%cpu | head -11" },
        { label: '监听端口', cmd: 'ss -lntup 2>/dev/null || netstat -lntup' }
      ]
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
    wsLabel() {
      return {
        idle: '未连接',
        connecting: '连接中',
        open: '已连接',
        closed: '已断开',
        error: '连接错误'
      }[this.wsState]
    }
  },
  methods: {
    formatTime,
    formatDuration,
    shortId,
    statusClass,
    statusText,
    isTerminal,
    downloadUrl(stream) {
      return this.target ? api.targets.downloadUrl(this.target.id, stream) : '#'
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
    goBack() {
      this.$router.push('/execute')
    },
    goFiles() {
      this.$router.push({ path: '/files', query: { server: this.serverId, path: '/' } })
    },
    goTerminal() {
      this.$router.push({ name: 'terminal', params: { id: this.serverId } })
    },
    goHistory() {
      this.$router.push({ path: '/history', query: { server_id: this.serverId } })
    },

    async run() {
      if (!this.form.command.trim()) {
        this.$message.warning('请输入要执行的命令')
        return
      }
      if (!this.serverId) return
      this.submitting = true
      this.disconnectWS()
      try {
        const res = await api.tasks.create({
          command: this.form.command,
          work_dir: this.form.work_dir,
          timeout_ms: this.form.timeout_sec * 1000,
          server_ids: [this.serverId]
        })
        this.task = res.task
        const t = (res.targets || []).find((x) => Number(x.server_id) === this.serverId)
        this.target = t ? { ...t, stdout: '', stderr: '' } : null
        this.lastSeq = {}
        this.manualClose = false
        this.$router.replace({ path: this.$route.path, query: { task: this.task.id } }).catch(() => {})
        if (this.target) this.connectWS(this.task.id)
        this.loadHistory()
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.submitting = false
      }
    },

    async loadTask(taskId) {
      this.disconnectWS()
      try {
        const res = await api.tasks.get(taskId)
        const t = (res.targets || []).find((x) => Number(x.server_id) === this.serverId)
        if (!t) {
          this.$message.warning('该任务不包含这台主机')
          return
        }
        this.task = res.task
        this.target = { ...t, stdout: '', stderr: '' }
        this.lastSeq = {}
        this.$router.replace({ path: this.$route.path, query: { task: taskId } }).catch(() => {})
        await this.loadPersistedLogs(this.target)
        if (isTerminal(res.task.status)) return
        this.manualClose = false
        this.connectWS(taskId)
      } catch (e) {
        this.$message.error('加载任务失败：' + e.message)
      }
    },

    // Read persisted output and return the resume sequence, so live frames
    // delivered after this point are not appended twice.
    async loadPersistedLogs(t) {
      let maxSeq = t.seq || 0
      for (const stream of ['stdout', 'stderr']) {
        let offset = 0
        for (let i = 0; i < 80; i++) {
          const res = await api.targets.logs(t.id, stream, offset, 262144)
          if (res.data) {
            const text = decodeBase64(res.data)
            if (stream === 'stdout') t.stdout += text
            else t.stderr += text
          }
          if (typeof res.seq === 'number' && res.seq > maxSeq) maxSeq = res.seq
          offset = res.next_offset
          if (res.eof) break
        }
      }
      this.lastSeq[t.id] = maxSeq
      return maxSeq
    },

    connectWS(taskId) {
      if (!taskId) return
      this.manualClose = false
      this.wsState = 'connecting'
      const ws = new WebSocket(wsUrl(`/ws/tasks/${taskId}`))
      this.ws = ws

      ws.onopen = () => {
        this.wsState = 'open'
        const resume = this.lastSeq || {}
        if (Object.keys(resume).length) {
          ws.send(JSON.stringify({ type: 'subscribe', resume }))
        }
        this.startPing()
      }
      ws.onmessage = (ev) => {
        let frame
        try {
          frame = JSON.parse(ev.data)
        } catch (e) {
          return
        }
        this.handleFrame(frame)
      }
      ws.onerror = () => {
        this.wsState = 'error'
      }
      ws.onclose = () => {
        this.stopPing()
        if (this.manualClose) return
        this.wsState = 'closed'
        if (this.task && !isTerminal(this.task.status)) this.scheduleReconnect()
      }
    },
    scheduleReconnect() {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = setTimeout(() => {
        if (this.task && !this.manualClose) this.connectWS(this.task.id)
      }, 2000)
    },
    startPing() {
      this.stopPing()
      this.pingTimer = setInterval(() => {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
          this.ws.send(JSON.stringify({ type: 'ping' }))
        }
      }, 25000)
    },
    stopPing() {
      if (this.pingTimer) {
        clearInterval(this.pingTimer)
        this.pingTimer = null
      }
    },
    handleFrame(frame) {
      if (frame.type === 'snapshot') {
        if (this.task && frame.status) this.task.status = frame.status
        const snap = (frame.targets || []).find(
          (s) => this.target && s.target_id === this.target.id
        )
        if (snap && snap.status) this.target.status = snap.status
        return
      }
      if (!this.target) return
      if (frame.type === 'stdout' || frame.type === 'stderr') {
        if (frame.target_id !== this.target.id) return
        const last = this.lastSeq[frame.target_id] || 0
        if (frame.seq && frame.seq <= last) return
        const text = decodeBase64(frame.data)
        if (frame.type === 'stdout') this.target.stdout += text
        else this.target.stderr += text
        this.lastSeq[frame.target_id] = frame.seq
        this.$nextTick(() => this.scroll(frame.type))
        return
      }
      if (frame.type === 'status') {
        if (frame.target_id !== this.target.id) return
        this.target.status = frame.status || this.target.status
        if (frame.exit_code !== undefined) this.target.exit_code = frame.exit_code
        if (frame.duration_ms !== undefined) this.target.duration_ms = frame.duration_ms
        if (frame.error !== undefined) this.target.error = frame.error
        return
      }
      if (frame.type === 'task_status') {
        if (this.task) this.task.status = frame.status
        if (isTerminal(frame.status)) this.loadHistory()
      }
    },
    scroll(stream) {
      const el = document.getElementById((stream === 'stderr' ? 'err-' : 'out-') + this.target.id)
      if (el) el.scrollTop = el.scrollHeight
    },
    disconnectWS() {
      clearTimeout(this.reconnectTimer)
      this.stopPing()
      if (this.ws) {
        this.manualClose = true
        try {
          this.ws.close()
        } catch (e) {
          /* ignore */
        }
        this.ws = null
      }
    },
    reconnectNow() {
      if (!this.task) return
      this.disconnectWS()
      this.connectWS(this.task.id)
    },

    async cancelTarget() {
      if (!this.target) return
      try {
        await api.targets.cancel(this.target.id)
        this.$message.success('已发送取消请求')
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async retryTarget() {
      if (!this.target) return
      try {
        const res = await api.targets.retry(this.target.id)
        this.$message.success(`已创建重试任务 ${shortId(res.task.id)}`)
        await this.loadTask(res.task.id)
        this.loadHistory()
      } catch (e) {
        this.$message.error(e.message)
      }
    },

    async loadHistory() {
      if (!this.serverId) return
      this.loadingHistory = true
      try {
        const data = await api.tasks.list({ server_id: this.serverId, page: 1, page_size: 8 })
        this.history = data.items || []
      } catch (e) {
        /* silent: history is secondary */
      } finally {
        this.loadingHistory = false
      }
    }
  },
  created() {
    this.loadServer()
    this.loadHistory()
    this.$store
      .dispatch('loadLimits')
      .then((l) => {
        this.limits = l || {}
        if (l && l.default_timeout_ms) this.form.timeout_sec = Math.floor(l.default_timeout_ms / 1000)
      })
      .catch(() => {})
    if (this.$route.query.task) this.loadTask(String(this.$route.query.task))
  },
  beforeDestroy() {
    this.disconnectWS()
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
.back-btn {
  flex: 0 0 auto;
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
.quick-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin: -4px 0 14px 86px;
}
.quick-hint {
  font-size: 11.5px;
  margin-left: 6px;
}
.run-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  border-top: 1px solid var(--border-soft);
  padding-top: 14px;
}
.term-label {
  font-size: 11px;
  letter-spacing: 1px;
  text-transform: uppercase;
  color: var(--muted);
  margin: 8px 0 6px;
}
.err-term {
  color: var(--term-err);
  max-height: 200px;
}
@media (max-width: 760px) {
  .quick-row {
    margin-left: 0;
  }
}
</style>
