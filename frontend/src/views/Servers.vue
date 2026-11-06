<template>
  <div>
    <div class="panel">
      <div class="toolbar">
        <el-input v-model="query.q" placeholder="搜索名称 / IP / 主机名" clearable style="width: 220px" @keyup.enter.native="reload" />
        <el-select v-model="query.status" placeholder="全部状态" clearable style="width: 130px">
          <el-option label="在线" value="online" />
          <el-option label="离线" value="offline" />
          <el-option label="未知" value="unknown" />
        </el-select>
        <el-select v-model="query.group_id" placeholder="全部分组" clearable style="width: 150px">
          <el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id" />
        </el-select>
        <el-select v-model="query.tag" placeholder="全部标签" clearable style="width: 150px">
          <el-option v-for="t in tags" :key="t.id" :label="t.name" :value="t.name" />
        </el-select>
        <el-button icon="el-icon-search" @click="reload">查询</el-button>
        <div class="spacer"></div>
        <el-button
          icon="el-icon-refresh"
          :disabled="!selected.length"
          :loading="checkingUpdates"
          @click="checkUpdates"
        >检查更新</el-button>
        <el-button
          type="warning"
          icon="el-icon-upload2"
          :disabled="!selected.length"
          :loading="upgrading"
          @click="batchUpgrade"
        >批量升级{{ selected.length ? ` (${selected.length})` : '' }}</el-button>
        <el-button icon="el-icon-collection" @click="manageVisible = true">分组 / 标签</el-button>
        <el-button type="primary" icon="el-icon-plus" @click="openCreate">新增服务器</el-button>
      </div>

      <el-table
        v-loading="loading"
        :data="servers"
        border
        empty-text="暂无服务器，请先新增"
        @selection-change="onSelectionChange"
      >
        <el-table-column type="selection" width="44" />
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="name" label="名称" min-width="140">
          <template slot-scope="{ row }"><strong>{{ row.name }}</strong></template>
        </el-table-column>
        <el-table-column label="Agent 地址" min-width="170">
          <template slot-scope="{ row }"><span class="mono">{{ row.ip }}:{{ row.agent_port }}</span></template>
        </el-table-column>
        <el-table-column prop="hostname" label="主机名" min-width="140">
          <template slot-scope="{ row }">{{ row.hostname || '-' }}</template>
        </el-table-column>
        <el-table-column label="系统 / 架构" width="150">
          <template slot-scope="{ row }">
            <span class="muted">{{ row.os || '-' }} / {{ row.arch || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="分组" width="120">
          <template slot-scope="{ row }">{{ groupName(row.group_id) }}</template>
        </el-table-column>
        <el-table-column label="标签" min-width="140">
          <template slot-scope="{ row }">
            <el-tag v-for="t in row.tags" :key="t.id" size="mini" style="margin-right: 4px">{{ t.name }}</el-tag>
            <span v-if="!row.tags || !row.tags.length" class="muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
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
        <el-table-column label="操作" width="380" fixed="right">
          <template slot-scope="{ row }">
            <el-button type="text" @click="openExec(row)">执行</el-button>
            <el-button type="text" @click="openTerminal(row)">终端</el-button>
            <el-button type="text" :loading="checking[row.id]" @click="checkServer(row)">健康检查</el-button>
            <el-button
              v-if="row.status === 'online'"
              type="text"
              :loading="updating[row.id]"
              @click="upgradeServer(row)"
            >升级</el-button>
            <el-button type="text" @click="openEdit(row)">编辑</el-button>
            <el-button type="text" style="color: var(--danger)" @click="removeServer(row)">删除</el-button>
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

    <!-- create / edit -->
    <el-dialog :title="editing ? '编辑服务器' : '新增服务器'" :visible.sync="editVisible" width="520px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="例如 web-01" />
        </el-form-item>
        <el-form-item label="IP / 主机" required>
          <el-input v-model="form.ip" placeholder="10.0.0.11" />
        </el-form-item>
        <el-form-item label="Agent 端口">
          <el-input-number v-model="form.agent_port" :min="1" :max="65535" style="width: 100%" />
        </el-form-item>
        <el-form-item label="分组">
          <el-select v-model="form.group_id" placeholder="选择分组" clearable style="width: 100%">
            <el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="标签">
          <el-select
            v-model="form.tags"
            multiple
            filterable
            allow-create
            default-first-option
            placeholder="输入后回车可新建标签"
            style="width: 100%"
          >
            <el-option v-for="t in tags" :key="t.id" :label="t.name" :value="t.name" />
          </el-select>
        </el-form-item>
      </el-form>
      <span slot="footer">
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveServer">保存</el-button>
      </span>
    </el-dialog>

    <!-- group / tag management -->
    <el-dialog title="分组与标签管理" :visible.sync="manageVisible" width="620px">
      <el-tabs v-model="manageTab">
        <el-tab-pane label="分组" name="groups">
          <div class="toolbar">
            <el-input v-model="newGroup.name" placeholder="分组名称" style="width: 180px" />
            <el-input v-model="newGroup.description" placeholder="描述（可选）" style="width: 220px" />
            <el-button type="primary" @click="createGroup">新增分组</el-button>
          </div>
          <el-table :data="groups" border>
            <el-table-column prop="id" label="ID" width="70" />
            <el-table-column prop="name" label="名称" />
            <el-table-column prop="description" label="描述" show-overflow-tooltip />
            <el-table-column label="操作" width="90">
              <template slot-scope="{ row }">
                <el-button type="text" style="color: var(--danger)" @click="removeGroup(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>
        <el-tab-pane label="标签" name="tags">
          <div class="toolbar">
            <el-input v-model="newTag" placeholder="标签名称" style="width: 200px" />
            <el-button type="primary" @click="createTag">新增标签</el-button>
          </div>
          <el-table :data="tags" border>
            <el-table-column prop="id" label="ID" width="70" />
            <el-table-column prop="name" label="名称" />
            <el-table-column label="操作" width="90">
              <template slot-scope="{ row }">
                <el-button type="text" style="color: var(--danger)" @click="removeTag(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>
      </el-tabs>
    </el-dialog>

    <!-- agent update results -->
    <el-dialog :title="updateTitle" :visible.sync="updateVisible" width="720px">
      <el-table :data="updateResults" border empty-text="暂无结果">
        <el-table-column prop="name" label="服务器" min-width="150" />
        <el-table-column label="当前版本" width="120">
          <template slot-scope="{ row }"><span class="mono">{{ row.current || '-' }}</span></template>
        </el-table-column>
        <el-table-column label="最新版本" width="120">
          <template slot-scope="{ row }"><span class="mono">{{ row.latest || '-' }}</span></template>
        </el-table-column>
        <el-table-column label="结果" min-width="240">
          <template slot-scope="{ row }">
            <span v-if="row.error" style="color: var(--danger)">{{ row.error }}</span>
            <span v-else-if="row.updated" style="color: var(--success)">已升级，Agent 正在重启</span>
            <span v-else-if="row.available" style="color: var(--warn)">可升级</span>
            <span v-else class="muted">已是最新</span>
          </template>
        </el-table-column>
      </el-table>
      <span slot="footer">
        <el-button @click="updateVisible = false">关闭</el-button>
      </span>
    </el-dialog>
  </div>
</template>

<script>
import api from '@/api'
import { formatTime, statusClass, statusText } from '@/utils/format'

const emptyForm = () => ({ id: null, name: '', ip: '', agent_port: 19090, group_id: null, tags: [] })

export default {
  name: 'Servers',
  data() {
    return {
      loading: false,
      saving: false,
      servers: [],
      total: 0,
      groups: [],
      tags: [],
      query: { q: '', status: '', group_id: '', tag: '', page: 1, page_size: 20 },
      editVisible: false,
      editing: false,
      form: emptyForm(),
      checking: {},
      selected: [],
      updating: {},
      checkingUpdates: false,
      upgrading: false,
      updateVisible: false,
      updateTitle: '',
      updateResults: [],
      manageVisible: false,
      manageTab: 'groups',
      newGroup: { name: '', description: '' },
      newTag: ''
    }
  },
  methods: {
    formatTime,
    statusClass,
    statusText,
    groupName(id) {
      const g = this.groups.find((x) => x.id === id)
      return g ? g.name : '-'
    },
    async load() {
      this.loading = true
      try {
        const data = await api.servers.list(this.query)
        this.servers = data.items || []
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
    async loadMeta() {
      try {
        const [groups, tags] = await Promise.all([api.groups.list(), api.tags.list()])
        this.groups = groups.items || []
        this.tags = tags.items || []
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    openCreate() {
      this.editing = false
      this.form = emptyForm()
      this.editVisible = true
    },
    openExec(row) {
      this.$router.push({ name: 'server-execute', params: { id: row.id } })
    },
    openTerminal(row) {
      this.$router.push({ name: 'terminal', params: { id: row.id } })
    },
    openEdit(row) {
      this.editing = true
      this.form = {
        id: row.id,
        name: row.name,
        ip: row.ip,
        agent_port: row.agent_port,
        group_id: row.group_id,
        tags: (row.tags || []).map((t) => t.name)
      }
      this.editVisible = true
    },
    async saveServer() {
      if (!this.form.name || !this.form.ip) {
        this.$message.warning('名称与 IP 为必填项')
        return
      }
      this.saving = true
      try {
        if (this.editing) {
          await api.servers.update(this.form.id, this.form)
          this.$message.success('已更新')
        } else {
          await api.servers.create(this.form)
          this.$message.success('已创建')
        }
        this.editVisible = false
        await Promise.all([this.load(), this.loadMeta()])
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.saving = false
      }
    },
    async removeServer(row) {
      try {
        await this.$confirm(`确认删除服务器「${row.name}」？`, '删除确认', { type: 'warning' })
      } catch (e) {
        return
      }
      try {
        await api.servers.remove(row.id)
        this.$message.success('已删除')
        this.load()
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async checkServer(row) {
      this.$set(this.checking, row.id, true)
      try {
        const res = await api.servers.check(row.id)
        if (res.online) {
          this.$notify.success({
            title: 'Agent 在线',
            message: `${res.info.hostname || row.ip} · ${res.info.os}/${res.info.arch} · v${res.info.version}`
          })
        } else {
          this.$notify.error({ title: 'Agent 离线', message: res.error || '无法连接' })
        }
        this.load()
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.$set(this.checking, row.id, false)
      }
    },
    onSelectionChange(rows) {
      this.selected = rows
    },
    async checkUpdates() {
      if (!this.selected.length) {
        this.$message.warning('请先勾选服务器')
        return
      }
      this.checkingUpdates = true
      try {
        const res = await api.servers.batchUpdateCheck(this.selected.map((s) => s.id))
        this.updateResults = res.items || []
        this.updateTitle = 'Agent 更新检查'
        this.updateVisible = true
        this.load()
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.checkingUpdates = false
      }
    },
    async batchUpgrade() {
      if (!this.selected.length) {
        this.$message.warning('请先勾选服务器')
        return
      }
      try {
        await this.$confirm(
          `将对 ${this.selected.length} 台服务器执行 Agent 升级，升级过程中 Agent 会重启，确定继续？`,
          '批量升级 Agent',
          { type: 'warning', confirmButtonText: '开始升级', cancelButtonText: '取消' }
        )
      } catch (e) {
        return
      }
      this.upgrading = true
      try {
        const res = await api.servers.batchUpdate(this.selected.map((s) => s.id))
        this.updateResults = res.items || []
        this.updateTitle = 'Agent 批量升级结果'
        this.updateVisible = true
        const failed = this.updateResults.filter((i) => !i.ok).length
        if (failed) this.$message.warning(`${failed} 台服务器升级失败，请查看结果`)
        else this.$message.success('升级指令已全部执行')
        setTimeout(() => this.load(), 1500)
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.upgrading = false
      }
    },
    async upgradeServer(row) {
      try {
        await this.$confirm(
          `将 Agent「${row.name}」升级到最新版本？升级过程中 Agent 会重启。`,
          '升级 Agent',
          { type: 'warning', confirmButtonText: '升级', cancelButtonText: '取消' }
        )
      } catch (e) {
        return
      }
      this.$set(this.updating, row.id, true)
      try {
        const res = await api.servers.applyUpdate(row.id)
        if (res.updated) {
          this.$notify.success({
            title: '已升级',
            message: `${res.from} → ${res.to}，Agent 正在重启`
          })
        } else {
          this.$notify.info({ title: '无需升级', message: res.reason || '已是最新版本' })
        }
        setTimeout(() => this.load(), 1500)
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.$set(this.updating, row.id, false)
      }
    },
    async createGroup() {
      if (!this.newGroup.name) {
        this.$message.warning('请输入分组名称')
        return
      }
      try {
        await api.groups.create(this.newGroup)
        this.newGroup = { name: '', description: '' }
        await this.loadMeta()
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async removeGroup(row) {
      try {
        await this.$confirm(`删除分组「${row.name}」？该组内服务器将变为未分组。`, '删除确认', { type: 'warning' })
      } catch (e) {
        return
      }
      try {
        await api.groups.remove(row.id)
        await Promise.all([this.loadMeta(), this.load()])
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async createTag() {
      if (!this.newTag) return
      try {
        await api.tags.create({ name: this.newTag })
        this.newTag = ''
        await this.loadMeta()
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async removeTag(row) {
      try {
        await api.tags.remove(row.id)
        await Promise.all([this.loadMeta(), this.load()])
      } catch (e) {
        this.$message.error(e.message)
      }
    }
  },
  created() {
    this.load()
    this.loadMeta()
  }
}
</script>
