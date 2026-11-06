<template>
  <div>
    <div v-loading="loading">
      <div class="stat-grid">
        <div class="stat-card">
          <div class="stat-label">受管服务器</div>
          <div class="stat-value">{{ data.servers_total || 0 }}</div>
          <div class="stat-hint">已登记主机总数</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">在线</div>
          <div class="stat-value success">{{ data.servers_online || 0 }}</div>
          <div class="stat-hint">最近心跳成功</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">离线</div>
          <div class="stat-value danger">{{ data.servers_offline || 0 }}</div>
          <div class="stat-hint">心跳失败或未知</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">任务成功率</div>
          <div class="stat-value accent">{{ successRate }}%</div>
          <div class="stat-hint">{{ data.tasks_success || 0 }} / {{ data.tasks_total || 0 }} 个任务</div>
        </div>
      </div>

      <div class="panel">
        <h3 class="panel-title"><span class="accent">●</span> Agent 版本分布</h3>
        <el-table v-if="versions.length" :data="versions" border>
          <el-table-column prop="version" label="Agent 版本" min-width="220">
            <template slot-scope="{ row }">
              <span class="mono">{{ row.version || '未知' }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="count" label="主机数量" width="160" align="center" />
        </el-table>
        <div v-else class="empty-hint">暂无 Agent 版本数据（请先添加服务器并完成心跳）</div>
      </div>

      <div class="panel">
        <h3 class="panel-title"><span class="accent">●</span> 近期任务</h3>
        <el-table v-if="recent.length" :data="recent" border>
          <el-table-column label="任务" width="110">
            <template slot-scope="{ row }"><span class="mono">{{ shortId(row.id) }}</span></template>
          </el-table-column>
          <el-table-column prop="command" label="命令" min-width="260" show-overflow-tooltip>
            <template slot-scope="{ row }"><span class="mono">{{ row.command }}</span></template>
          </el-table-column>
          <el-table-column label="状态" width="120">
            <template slot-scope="{ row }">
              <span class="status-badge" :class="statusClass(row.status)">{{ statusText(row.status) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="成功/失败" width="120" align="center">
            <template slot-scope="{ row }">
              <span style="color: var(--success)">{{ row.succeeded }}</span>
              <span class="muted"> / </span>
              <span style="color: var(--danger)">{{ row.failed }}</span>
            </template>
          </el-table-column>
          <el-table-column label="创建时间" width="180">
            <template slot-scope="{ row }">{{ formatTime(row.created_at) }}</template>
          </el-table-column>
        </el-table>
        <div v-else class="empty-hint">暂无任务记录</div>
      </div>
    </div>
  </div>
</template>

<script>
import api from '@/api'
import { formatTime, shortId, statusClass, statusText } from '@/utils/format'

export default {
  name: 'Dashboard',
  data() {
    return { loading: false, data: {} }
  },
  computed: {
    versions() {
      return this.data.agent_versions || []
    },
    recent() {
      return this.data.recent_tasks || []
    },
    successRate() {
      return (this.data.success_rate || 0).toFixed(1)
    }
  },
  methods: { formatTime, shortId, statusClass, statusText },
  async created() {
    this.loading = true
    try {
      this.data = await api.overview()
    } catch (e) {
      this.$message.error(e.message)
    } finally {
      this.loading = false
    }
  }
}
</script>
