<template>
  <div>
    <div class="panel">
      <div class="toolbar">
        <el-select v-model="query.status" placeholder="全部状态" clearable style="width: 140px">
          <el-option v-for="s in statuses" :key="s" :label="statusText(s)" :value="s" />
        </el-select>
        <el-select v-model="query.server_id" placeholder="全部服务器" clearable filterable style="width: 180px">
          <el-option v-for="s in servers" :key="s.id" :label="s.name" :value="s.id" />
        </el-select>
        <el-date-picker
          v-model="query.date"
          type="datetimerange"
          range-separator="至"
          start-placeholder="开始时间"
          end-placeholder="结束时间"
          style="width: 360px"
        />
        <el-button icon="el-icon-search" @click="reload">查询</el-button>
        <div class="spacer"></div>
        <el-button icon="el-icon-refresh" @click="load">刷新</el-button>
      </div>

      <el-table v-loading="loading" :data="tasks" border empty-text="暂无任务记录">
        <el-table-column label="任务" width="100">
          <template slot-scope="{ row }"><span class="mono">{{ shortId(row.id) }}</span></template>
        </el-table-column>
        <el-table-column prop="command" label="命令" min-width="300" show-overflow-tooltip>
          <template slot-scope="{ row }"><span class="mono">{{ row.command }}</span></template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template slot-scope="{ row }">
            <span class="status-badge" :class="statusClass(row.status)">{{ statusText(row.status) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="总数" width="70" align="center">
          <template slot-scope="{ row }">{{ row.total }}</template>
        </el-table-column>
        <el-table-column label="成功 / 失败 / 取消" width="170" align="center">
          <template slot-scope="{ row }">
            <span style="color: var(--success)">{{ row.succeeded }}</span>
            <span class="muted"> / </span>
            <span style="color: var(--danger)">{{ row.failed }}</span>
            <span class="muted"> / </span>
            <span style="color: var(--warn)">{{ row.cancelled }}</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template slot-scope="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="结束时间" width="170">
          <template slot-scope="{ row }">{{ formatTime(row.finished_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template slot-scope="{ row }">
            <el-button type="text" @click="openDetail(row)">详情</el-button>
            <el-button type="text" style="color: var(--warn)" @click="cancelTask(row)" :disabled="isTerminal(row.status)">
              取消
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <div style="margin-top: 14px; text-align: right">
        <el-pagination
          background
          layout="total, sizes, prev, pager, next"
          :total="total"
          :current-page.sync="query.page"
          :page-size.sync="query.page_size"
          :page-sizes="[10, 20, 50, 100]"
          @current-change="load"
          @size-change="reload"
        />
      </div>
    </div>

    <el-drawer :visible.sync="drawer" size="72%" title="任务详情" :wrapper-closable="true">
      <div v-loading="detailLoading">
        <div class="panel" v-if="current">
          <div class="toolbar">
            <span class="status-badge" :class="statusClass(current.status)">{{ statusText(current.status) }}</span>
            <span class="mono muted">任务 {{ current.id }}</span>
            <div class="spacer"></div>
            <el-button size="mini" icon="el-icon-refresh" @click="refreshDetail">刷新</el-button>
            <el-button size="mini" icon="el-icon-video-pause" :disabled="isTerminal(current.status)" @click="cancelTask(current)">
              取消剩余
            </el-button>
          </div>
          <el-descriptions :column="3" border>
            <el-descriptions-item label="命令" :span="3">
              <span class="mono">{{ current.command }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="工作目录">{{ current.work_dir || '-' }}</el-descriptions-item>
            <el-descriptions-item label="超时">{{ current.timeout_ms }} ms</el-descriptions-item>
            <el-descriptions-item label="操作人">{{ current.created_by }}</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(current.created_at) }}</el-descriptions-item>
            <el-descriptions-item label="结束时间">{{ formatTime(current.finished_at) }}</el-descriptions-item>
            <el-descriptions-item label="统计">
              总 {{ current.total }} · 成功 {{ current.succeeded }} · 失败 {{ current.failed }} · 取消 {{ current.cancelled }}
            </el-descriptions-item>
          </el-descriptions>
        </div>

        <div class="panel">
          <h3 class="panel-title"><span class="accent">●</span> 目标主机</h3>
          <el-table :data="currentTargets" border size="mini">
            <el-table-column prop="server_name" label="主机" min-width="140" />
            <el-table-column label="状态" width="110">
              <template slot-scope="{ row }">
                <span class="status-badge" :class="statusClass(row.status)">{{ statusText(row.status) }}</span>
              </template>
            </el-table-column>
            <el-table-column label="退出码" width="90" align="center">
              <template slot-scope="{ row }">{{ row.exit_code === null ? '-' : row.exit_code }}</template>
            </el-table-column>
            <el-table-column label="耗时" width="110">
              <template slot-scope="{ row }">{{ formatDuration(row.duration_ms) }}</template>
            </el-table-column>
            <el-table-column label="错误" min-width="160" show-overflow-tooltip>
              <template slot-scope="{ row }"><span style="color: var(--danger)">{{ row.error || '-' }}</span></template>
            </el-table-column>
            <el-table-column label="操作" width="230">
              <template slot-scope="{ row }">
                <el-button type="text" @click="viewLogs(row)">查看日志</el-button>
                <el-button type="text" :disabled="isTerminal(row.status)" @click="cancelTarget(row)">取消</el-button>
                <el-button type="text" style="color: var(--warn)" :disabled="!isTerminal(row.status)" @click="retryTarget(row)">
                  重试
                </el-button>
                <el-button type="text" tag="a" :href="downloadUrl(row, 'stdout')" target="_blank">下载</el-button>
              </template>
            </el-table-column>
          </el-table>
        </div>

        <div v-if="logTarget" class="panel">
          <div class="toolbar">
            <h3 class="panel-title" style="margin: 0">
              <span class="accent">●</span> 日志 · {{ logTarget.server_name }}
            </h3>
            <div class="spacer"></div>
            <el-radio-group v-model="logTab" size="mini" @change="loadCurrentLog">
              <el-radio-button label="stdout">stdout</el-radio-button>
              <el-radio-button label="stderr">stderr</el-radio-button>
            </el-radio-group>
            <el-button size="mini" icon="el-icon-refresh" :loading="logLoading" @click="loadCurrentLog">重新加载</el-button>
            <el-button size="mini" type="text" tag="a" :href="downloadUrl(logTarget, logTab)" target="_blank">下载完整日志</el-button>
          </div>
          <div v-if="!logLoading && !logText[logTab]" class="empty-hint">该流暂无输出</div>
          <div v-else class="term" style="max-height: 520px">{{ logText[logTab] }}</div>
          <div v-if="logEof && logText[logTab]" class="muted" style="margin-top: 8px; font-size: 12px">
            （已加载 1MB 以内内容；完整内容请下载）
          </div>
        </div>
      </div>
    </el-drawer>
  </div>
</template>

<script>
import api from '@/api'
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
  name: 'History',
  data() {
    return {
      loading: false,
      tasks: [],
      total: 0,
      servers: [],
      statuses: ['PENDING', 'DISPATCHING', 'RUNNING', 'SUCCESS', 'FAILED', 'TIMEOUT', 'CANCELLED', 'UNKNOWN'],
      query: { status: '', server_id: '', date: [], page: 1, page_size: 20 },
      drawer: false,
      detailLoading: false,
      current: null,
      currentTargets: [],
      logTarget: null,
      logTab: 'stdout',
      logText: { stdout: '', stderr: '' },
      logLoading: false,
      logEof: { stdout: true, stderr: true }
    }
  },
  methods: {
    formatTime,
    formatDuration,
    shortId,
    statusClass,
    statusText,
    isTerminal,
    downloadUrl(t, stream) {
      return api.targets.downloadUrl(t.id, stream)
    },
    buildParams() {
      const p = { page: this.query.page, page_size: this.query.page_size }
      if (this.query.status) p.status = this.query.status
      if (this.query.server_id) p.server_id = this.query.server_id
      if (this.query.date && this.query.date.length === 2) {
        p.from = new Date(this.query.date[0]).toISOString()
        p.to = new Date(this.query.date[1]).toISOString()
      }
      return p
    },
    async load() {
      this.loading = true
      try {
        const data = await api.tasks.list(this.buildParams())
        this.tasks = data.items || []
        this.total = data.total || 0
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.loading = false
      }
    },
    reload() {
      this.query.page = 1
      this.load()
    },
    async loadServers() {
      try {
        const data = await api.servers.list({ page: 1, page_size: 200 })
        this.servers = data.items || []
      } catch (e) {
        /* ignore */
      }
    },
    async openDetail(row) {
      this.drawer = true
      this.logTarget = null
      this.logText = { stdout: '', stderr: '' }
      await this.fetchDetail(row.id)
    },
    async fetchDetail(id) {
      this.detailLoading = true
      try {
        const res = await api.tasks.get(id)
        this.current = res.task
        this.currentTargets = res.targets || []
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.detailLoading = false
      }
    },
    refreshDetail() {
      if (this.current) this.fetchDetail(this.current.id)
    },
    async viewLogs(target) {
      this.logTarget = target
      this.logTab = 'stdout'
      await this.loadCurrentLog()
    },
    async loadCurrentLog() {
      if (!this.logTarget) return
      this.logLoading = true
      try {
        const res = await api.targets.logs(this.logTarget.id, this.logTab, 0, 1048576)
        this.logText = { ...this.logText, [this.logTab]: decodeBase64(res.data) }
        this.logEof = { ...this.logEof, [this.logTab]: res.eof }
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.logLoading = false
      }
    },
    async cancelTask(row) {
      try {
        await api.tasks.cancel(row.id)
        this.$message.success('已发送取消请求')
        if (this.drawer && this.current) this.fetchDetail(this.current.id)
        this.load()
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async cancelTarget(row) {
      try {
        await api.targets.cancel(row.id)
        this.$message.success('已发送取消请求')
        this.refreshDetail()
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async retryTarget(row) {
      try {
        const res = await api.targets.retry(row.id)
        this.$message.success(`已创建重试任务 ${shortId(res.task.id)}`)
        this.load()
      } catch (e) {
        this.$message.error(e.message)
      }
    }
  },
  created() {
    const sid = parseInt(this.$route.query.server_id, 10)
    if (!Number.isNaN(sid)) this.query.server_id = sid
    this.load()
    this.loadServers()
  }
}
</script>
