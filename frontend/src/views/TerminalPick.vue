<template>
  <div>
    <div class="panel">
      <div class="toolbar">
        <el-input
          v-model="keyword"
          placeholder="搜索名称 / IP / 主机名"
          clearable
          style="width: 240px"
        />
        <el-select v-model="statusFilter" placeholder="全部状态" clearable style="width: 130px">
          <el-option label="在线" value="online" />
          <el-option label="离线" value="offline" />
        </el-select>
        <el-button icon="el-icon-refresh" :loading="loading" @click="load">刷新</el-button>
        <div class="spacer"></div>
        <span class="muted">
          交互式终端为实时 PTY 会话，不产生平台任务记录；仅审计会话打开/关闭。
        </span>
      </div>

      <el-table
        v-loading="loading"
        :data="filtered"
        border
        empty-text="没有匹配的服务器"
        class="clickable-rows"
        @row-dblclick="open"
      >
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="name" label="名称" min-width="160">
          <template slot-scope="{ row }"><strong>{{ row.name }}</strong></template>
        </el-table-column>
        <el-table-column label="Agent 地址" min-width="170">
          <template slot-scope="{ row }"><span class="mono">{{ row.ip }}:{{ row.agent_port }}</span></template>
        </el-table-column>
        <el-table-column prop="hostname" label="主机名" min-width="150">
          <template slot-scope="{ row }">{{ row.hostname || '-' }}</template>
        </el-table-column>
        <el-table-column label="系统 / 架构" width="150">
          <template slot-scope="{ row }">
            <span class="muted">{{ row.os || '-' }} / {{ row.arch || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template slot-scope="{ row }">
            <span class="status-badge" :class="statusClass(row.status)">{{ statusText(row.status) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="Agent 版本" width="120">
          <template slot-scope="{ row }"><span class="mono">{{ row.agent_version || '-' }}</span></template>
        </el-table-column>
        <el-table-column label="最后心跳" width="170">
          <template slot-scope="{ row }">{{ formatTime(row.last_heartbeat_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="120" fixed="right">
          <template slot-scope="{ row }">
            <el-button type="text" icon="el-icon-connection" @click.stop="open(row)">打开终端</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="pick-foot muted">
        双击任意一行也可打开终端。主机离线时仍可尝试连接，失败会显示 Agent 返回的错误。
      </div>
    </div>
  </div>
</template>

<script>
import api from '@/api'
import { formatTime, statusClass, statusText } from '@/utils/format'

export default {
  name: 'TerminalPick',
  data() {
    return {
      loading: false,
      servers: [],
      keyword: '',
      statusFilter: ''
    }
  },
  computed: {
    filtered() {
      const kw = this.keyword.trim().toLowerCase()
      return this.servers.filter((s) => {
        if (this.statusFilter && s.status !== this.statusFilter) return false
        if (!kw) return true
        return [s.name, s.ip, s.hostname]
          .filter(Boolean)
          .some((v) => String(v).toLowerCase().includes(kw))
      })
    }
  },
  methods: {
    formatTime,
    statusClass,
    statusText,
    async load() {
      this.loading = true
      try {
        const res = await api.servers.list({ page: 1, page_size: 200 })
        this.servers = res.items || res || []
      } catch (e) {
        this.$message.error(e.message || '加载服务器失败')
      } finally {
        this.loading = false
      }
    },
    open(row) {
      if (!row || !row.id) return
      this.$router.push(`/servers/${row.id}/terminal`)
    }
  },
  created() {
    this.load()
  }
}
</script>

<style scoped>
.pick-foot {
  margin-top: 12px;
  font-size: 11.5px;
}
</style>
