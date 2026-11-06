<template>
  <div>
    <div class="panel">
      <h3 class="panel-title"><span class="accent">●</span> 执行配置</h3>
      <div class="exec-grid">
        <div>
          <div class="toolbar" style="margin-bottom: 10px">
            <el-input v-model="filter.q" placeholder="筛选服务器" clearable size="small" style="width: 180px" />
            <el-select v-model="filter.group_id" placeholder="全部分组" clearable size="small" style="width: 140px">
              <el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id" />
            </el-select>
            <span class="muted">已选 {{ selectedIds.length }} 台 · 点击主机名进入单机页面</span>
          </div>
          <el-table
            v-loading="loadingServers"
            :data="filteredServers"
            height="290"
            border
            size="mini"
            class="clickable-rows"
            empty-text="暂无服务器"
            @selection-change="onSelectionChange"
            @row-click="openServer"
          >
            <el-table-column type="selection" width="46" />
            <el-table-column prop="name" label="服务器" min-width="130">
              <template slot-scope="{ row }">
                <router-link
                  class="srv-link"
                  :to="{ name: 'server-execute', params: { id: row.id } }"
                  @click.native.stop
                ><strong>{{ row.name }}</strong></router-link>
                <span class="muted mono" style="margin-left: 6px">{{ row.ip }}</span>
                <i class="el-icon-right srv-go"></i>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="90">
              <template slot-scope="{ row }">
                <span class="status-badge" :class="statusClass(row.status)">{{ statusText(row.status) }}</span>
              </template>
            </el-table-column>
            <el-table-column label="版本" width="90">
              <template slot-scope="{ row }"><span class="mono">{{ row.agent_version || '-' }}</span></template>
            </el-table-column>
          </el-table>
        </div>

        <div>
          <el-form label-width="86px">
            <el-form-item label="命令">
              <el-input
                v-model="form.command"
                type="textarea"
                :rows="7"
                placeholder="例如：df -h&#10;支持多行脚本、管道、环境变量"
              />
            </el-form-item>
            <el-form-item label="工作目录">
              <el-input v-model="form.work_dir" placeholder="留空使用 Agent 默认目录" />
            </el-form-item>
            <el-form-item label="超时(秒)">
              <el-input-number v-model="form.timeout_sec" :min="1" :max="3600" style="width: 160px" />
              <span class="muted" style="margin-left: 10px">全局并发上限 {{ limits.dispatch_concurrency || '-' }}</span>
            </el-form-item>
          </el-form>
          <div style="text-align: right">
            <el-button type="primary" icon="el-icon-video-play" :loading="submitting" @click="submit">
              执行到 {{ selectedIds.length }} 台主机
            </el-button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="task" class="panel">
      <div class="toolbar">
        <h3 class="panel-title" style="margin: 0"><span class="accent">●</span> 执行结果</h3>
        <span class="status-badge" :class="statusClass(task.status)">{{ statusText(task.status) }}</span>
        <span class="muted mono">任务 {{ shortId(task.id) }}</span>
        <span class="muted">连接：{{ wsLabel }}</span>
        <div class="spacer"></div>
        <el-button size="mini" icon="el-icon-refresh" @click="reconnectNow">重连</el-button>
        <el-button size="mini" type="warning" icon="el-icon-circle-close" @click="cancelTask">取消全部</el-button>
      </div>

      <div class="exec-summary">
        <div class="summary-item"><span class="muted">总数</span><strong>{{ targets.length }}</strong></div>
        <div class="summary-item"><span class="muted">成功</span><strong style="color: var(--success)">{{ counts.success }}</strong></div>
        <div class="summary-item"><span class="muted">失败</span><strong style="color: var(--danger)">{{ counts.failed }}</strong></div>
        <div class="summary-item"><span class="muted">取消</span><strong style="color: var(--warn)">{{ counts.cancelled }}</strong></div>
        <div class="summary-item"><span class="muted">进行中</span><strong style="color: var(--accent)">{{ counts.running }}</strong></div>
      </div>

      <el-tabs v-model="activeTarget">
        <el-tab-pane v-for="t in targets" :key="t.id" :name="t.id">
          <span slot="label">
            <span class="status-badge" :class="statusClass(t.status)">{{ statusText(t.status) }}</span>
            <span style="margin-left: 6px">{{ t.server_name }}</span>
          </span>
          <div class="term-toolbar">
            <span class="term-meta">主机：<span class="mono">{{ t.agent_addr || t.server_name }}</span></span>
            <span class="term-meta">退出码：<span class="mono">{{ t.exit_code === null || t.exit_code === undefined ? '-' : t.exit_code }}</span></span>
            <span class="term-meta">耗时：{{ formatDuration(t.duration_ms) }}</span>
            <span v-if="t.error" class="term-meta" style="color: var(--danger)">{{ t.error }}</span>
            <div class="spacer"></div>
            <el-button size="mini" @click="cancelTarget(t)" :disabled="isTerminal(t.status)">取消</el-button>
            <el-button size="mini" @click="retryTarget(t)" :disabled="!isTerminal(t.status)">重试</el-button>
            <el-button size="mini" type="text" tag="a" :href="downloadUrl(t, 'stdout')" target="_blank">下载 stdout</el-button>
            <el-button size="mini" type="text" tag="a" :href="downloadUrl(t, 'stderr')" target="_blank">下载 stderr</el-button>
          </div>

          <div class="term-label">stdout</div>
          <div class="term" :id="'out-' + t.id">{{ t.stdout || '（等待输出…）' }}</div>
          <template v-if="t.stderr">
            <div class="term-label" style="color: var(--danger)">stderr</div>
            <div class="term err-term" :id="'err-' + t.id">{{ t.stderr }}</div>
          </template>
        </el-tab-pane>
      </el-tabs>
    </div>
  </div>
</template>

<script>
import api from '@/api'
import { wsUrl } from '@/api/base'
import {
  formatDuration,
  isTerminal,
  statusClass,
  statusText,
  shortId,
  decodeBase64
} from '@/utils/format'

export default {
  name: 'Execute',
  data() {
    return {
      servers: [],
      groups: [],
      loadingServers: false,
      selectedIds: [],
      filter: { q: '', group_id: '' },
      form: { command: '', work_dir: '', timeout_sec: 300 },
      limits: {},
      submitting: false,
      restoring: false,
      task: null,
      targets: [],
      activeTarget: '',
      ws: null,
      wsState: 'idle',
      reconnectTimer: null,
      pingTimer: null,
      manualClose: false,
      lastSeq: {},
      autoScroll: true
    }
  },
  computed: {
    filteredServers() {
      return this.servers.filter((s) => {
        if (this.filter.group_id && s.group_id !== this.filter.group_id) return false
        if (!this.filter.q) return true
        const q = this.filter.q.toLowerCase()
        return (
          (s.name || '').toLowerCase().includes(q) ||
          (s.ip || '').toLowerCase().includes(q) ||
          (s.hostname || '').toLowerCase().includes(q)
        )
      })
    },
    counts() {
      const c = { success: 0, failed: 0, cancelled: 0, running: 0 }
      this.targets.forEach((t) => {
        if (t.status === 'SUCCESS') c.success++
        else if (t.status === 'CANCELLED') c.cancelled++
        else if (isTerminal(t.status)) c.failed++
        else c.running++
      })
      return c
    },
    wsLabel() {
      return { idle: '未连接', connecting: '连接中', open: '已连接', closed: '已断开', error: '连接错误' }[
        this.wsState
      ]
    },
    resumeMap() {
      return this.lastSeq
    }
  },
  methods: {
    formatDuration,
    isTerminal,
    statusClass,
    statusText,
    shortId,
    downloadUrl(t, stream) {
      return api.targets.downloadUrl(t.id, stream)
    },
    onSelectionChange(rows) {
      this.selectedIds = rows.map((r) => r.id)
    },
    // Row click opens the per-server page; checkbox / link clicks keep their
    // own behavior.
    openServer(row, column, event) {
      const el = event && event.target
      if (el && el.closest && (el.closest('.el-table-column--selection') || el.closest('.el-checkbox') || el.closest('a'))) {
        return
      }
      this.$router.push({ name: 'server-execute', params: { id: row.id } })
    },
    async loadServers() {
      this.loadingServers = true
      try {
        const data = await api.servers.list({ page: 1, page_size: 200 })
        this.servers = data.items || []
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.loadingServers = false
      }
    },
    async loadGroups() {
      try {
        const data = await api.groups.list()
        this.groups = data.items || []
      } catch (e) {
        /* ignore */
      }
    },
    async submit() {
      if (this.restoring) {
        this.$message.info('正在恢复上一个任务，请稍候')
        return
      }
      if (!this.selectedIds.length) {
        this.$message.warning('请至少选择一台服务器')
        return
      }
      if (!this.form.command.trim()) {
        this.$message.warning('请输入要执行的命令')
        return
      }
      this.submitting = true
      this.disconnectWS()
      try {
        const res = await api.tasks.create({
          command: this.form.command,
          work_dir: this.form.work_dir,
          timeout_ms: this.form.timeout_sec * 1000,
          server_ids: this.selectedIds
        })
        this.task = res.task
        this.targets = (res.targets || []).map((t) => ({
          ...t,
          stdout: '',
          stderr: ''
        }))
        this.activeTarget = this.targets.length ? this.targets[0].id : ''
        this.lastSeq = {}
        this.manualClose = false
        this.$router.replace({ path: '/execute', query: { task: this.task.id } }).catch(() => {})
        this.connectWS(this.task.id)
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.submitting = false
      }
    },
    async restoreTask(taskId) {
      this.restoring = true
      try {
        const res = await api.tasks.get(taskId)
        this.task = res.task
        this.targets = (res.targets || []).map((t) => ({ ...t, stdout: '', stderr: '' }))
        this.activeTarget = this.targets.length ? this.targets[0].id : ''
        this.lastSeq = {}
        const seqs = await Promise.all(this.targets.map((t) => this.loadPersistedLogs(t)))
        this.targets.forEach((t, i) => {
          if (seqs[i] > 0) this.lastSeq[t.id] = seqs[i]
        })
        if (this._isDestroyed) return
        this.manualClose = false
        this.connectWS(taskId)
      } catch (e) {
        this.$message.error('恢复任务失败：' + e.message)
        this.$router.replace({ path: '/execute' }).catch(() => {})
      } finally {
        this.restoring = false
      }
    },
    // Loads a target's persisted output and returns the resume sequence read
    // after the file, so live frames after this point are not duplicated.
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
      return maxSeq
    },
    findTarget(id) {
      return this.targets.find((t) => t.id === id)
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
        if (this.task && !isTerminal(this.task.status)) {
          this.scheduleReconnect()
        }
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
        if (this.task) this.task.status = frame.status || this.task.status
        ;(frame.targets || []).forEach((snap) => {
          const local = this.findTarget(snap.target_id)
          if (local) {
            local.status = snap.status || local.status
            local.server_name = snap.server_name || local.server_name
          } else {
            this.targets.push({
              id: snap.target_id,
              server_id: snap.server_id,
              server_name: snap.server_name,
              status: snap.status,
              stdout: '',
              stderr: '',
              seq: snap.seq
            })
          }
        })
        if (!this.activeTarget && this.targets.length) this.activeTarget = this.targets[0].id
        return
      }
      if (frame.type === 'stdout' || frame.type === 'stderr') {
        const t = this.findTarget(frame.target_id)
        if (!t) return
        const last = this.lastSeq[frame.target_id] || 0
        if (frame.seq && frame.seq <= last) return
        const text = decodeBase64(frame.data)
        if (frame.type === 'stdout') t.stdout += text
        else t.stderr += text
        this.lastSeq[frame.target_id] = frame.seq
        this.$nextTick(() => this.scrollTarget(frame.target_id, frame.type))
        return
      }
      if (frame.type === 'status') {
        const t = this.findTarget(frame.target_id)
        if (!t) return
        t.status = frame.status || t.status
        if (frame.exit_code !== undefined) t.exit_code = frame.exit_code
        if (frame.duration_ms !== undefined) t.duration_ms = frame.duration_ms
        if (frame.error !== undefined) t.error = frame.error
        return
      }
      if (frame.type === 'task_status') {
        if (this.task) this.task.status = frame.status
        return
      }
    },
    scrollTarget(targetId, stream) {
      if (!this.autoScroll) return
      const el = document.getElementById((stream === 'stderr' ? 'err-' : 'out-') + targetId)
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
    async cancelTarget(t) {
      try {
        await api.targets.cancel(t.id)
        this.$message.success('已发送取消请求')
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async retryTarget(t) {
      try {
        const res = await api.targets.retry(t.id)
        this.$message.success(`已创建重试任务 ${shortId(res.task.id)}`)
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async cancelTask() {
      if (!this.task) return
      try {
        await api.tasks.cancel(this.task.id)
        this.$message.success('已发送批量取消请求')
      } catch (e) {
        this.$message.error(e.message)
      }
    }
  },
  created() {
    this.loadServers()
    this.loadGroups()
    if (this.$route.query.task) this.restoreTask(String(this.$route.query.task))
    this.$store
      .dispatch('loadLimits')
      .then((l) => {
        this.limits = l
        if (l && l.default_timeout_ms) this.form.timeout_sec = Math.floor(l.default_timeout_ms / 1000)
      })
      .catch(() => {})
  },
  beforeDestroy() {
    this.disconnectWS()
  }
}
</script>

<style scoped>
.exec-grid {
  display: grid;
  grid-template-columns: minmax(360px, 1fr) minmax(420px, 1.2fr);
  gap: 22px;
}
.exec-summary {
  display: flex;
  gap: 26px;
  padding: 12px 16px;
  margin-bottom: 14px;
  background: var(--bg-soft);
  border: 1px solid var(--border);
  border-radius: 10px;
}
.summary-item {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.summary-item strong {
  font-size: 18px;
}
.srv-link:hover strong {
  color: var(--accent);
  text-decoration: underline;
}
.srv-go {
  margin-left: 6px;
  font-size: 11px;
  color: var(--muted);
  opacity: 0.55;
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
</style>
