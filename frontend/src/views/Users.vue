<template>
  <div>
    <div class="panel">
      <div class="toolbar">
        <el-input
          v-model="query.q"
          placeholder="搜索用户名"
          clearable
          style="width: 220px"
        />
        <el-button icon="el-icon-refresh" :loading="loading" @click="load">刷新</el-button>
        <div class="spacer"></div>
        <el-button type="primary" icon="el-icon-plus" @click="openCreate">新增子用户</el-button>
      </div>

      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="管理员可见全部服务器；子用户只能看到并操作自己名下的服务器。删除账号前需先处理其名下服务器。"
        style="margin-bottom: 14px"
      />

      <el-table v-loading="loading" :data="filtered" border empty-text="暂无用户">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="username" label="用户名" min-width="150">
          <template slot-scope="{ row }">
            <strong>{{ row.username }}</strong>
            <el-tag v-if="row.id === meId" size="mini" style="margin-left: 8px">当前账号</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="角色" width="110">
          <template slot-scope="{ row }">
            <el-tag :type="row.role === 'admin' ? 'warning' : 'info'" size="mini">
              {{ row.role === 'admin' ? '管理员' : '子用户' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="server_count" label="名下服务器" width="110" />
        <el-table-column prop="created_by" label="创建人" width="130">
          <template slot-scope="{ row }">{{ row.created_by || '-' }}</template>
        </el-table-column>
        <el-table-column label="创建时间" width="180">
          <template slot-scope="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="260" fixed="right">
          <template slot-scope="{ row }">
            <el-button type="text" @click="openTokens(row)">令牌</el-button>
            <el-button
              type="text"
              :disabled="row.id === meId"
              @click="openReset(row)"
            >重置密码</el-button>
            <el-button
              type="text"
              :disabled="row.id === meId || row.role === 'admin'"
              style="color: var(--danger)"
              @click="removeUser(row)"
            >删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog title="新增子用户" :visible.sync="createVisible" width="440px" @closed="resetCreate">
      <el-form label-width="90px" @submit.native.prevent>
        <el-form-item label="用户名">
          <el-input v-model="createForm.username" placeholder="3-32 位字母、数字、_ . -" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="createForm.password" type="password" show-password placeholder="至少 6 位" />
        </el-form-item>
        <el-form-item label="确认密码">
          <el-input v-model="createForm.confirm" type="password" show-password />
        </el-form-item>
      </el-form>
      <div slot="footer">
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitCreate">创建</el-button>
      </div>
    </el-dialog>

    <el-dialog title="重置密码" :visible.sync="resetVisible" width="420px" @closed="resetReset">
      <el-form label-width="90px" @submit.native.prevent>
        <el-form-item label="用户">
          <span class="mono">{{ resetTarget.username }}</span>
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="resetForm.password" type="password" show-password placeholder="至少 6 位" />
        </el-form-item>
        <el-form-item label="确认密码">
          <el-input v-model="resetForm.confirm" type="password" show-password />
        </el-form-item>
      </el-form>
      <div slot="footer">
        <el-button @click="resetVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitReset">保存</el-button>
      </div>
    </el-dialog>

    <el-dialog
      :title="`API 令牌 · ${tokenTarget.username || ''}`"
      :visible.sync="tokensVisible"
      width="720px"
      @closed="resetTokens"
    >
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="管理员可以查看并吊销子用户的令牌，但看不到令牌明文（只在创建时对本人显示一次）。重置密码会吊销该用户全部令牌。"
        style="margin-bottom: 14px"
      />
      <el-table v-loading="tokensLoading" :data="tokens" border empty-text="该用户暂无令牌">
        <el-table-column prop="name" label="名称" min-width="120" />
        <el-table-column label="前缀" width="150">
          <template slot-scope="{ row }"><code>{{ row.prefix }}…</code></template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template slot-scope="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="过期时间" width="170">
          <template slot-scope="{ row }">
            {{ row.expires_at ? formatTime(row.expires_at) : '永不过期' }}
          </template>
        </el-table-column>
        <el-table-column label="最后使用" width="170">
          <template slot-scope="{ row }">
            {{ row.last_used_at ? formatTime(row.last_used_at) : '从未使用' }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="80" fixed="right">
          <template slot-scope="{ row }">
            <el-button type="text" style="color: var(--danger)" @click="revokeToken(row)">
              吊销
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>
  </div>
</template>

<script>
import api from '@/api'

export default {
  name: 'Users',
  data() {
    return {
      loading: false,
      saving: false,
      users: [],
      query: { q: '' },
      createVisible: false,
      createForm: { username: '', password: '', confirm: '' },
      resetVisible: false,
      resetTarget: {},
      resetForm: { password: '', confirm: '' },
      tokensVisible: false,
      tokensLoading: false,
      tokenTarget: {},
      tokens: []
    }
  },
  computed: {
    meId() {
      const u = this.$store.state.user
      return u ? u.id : 0
    },
    filtered() {
      const q = (this.query.q || '').trim().toLowerCase()
      if (!q) return this.users
      return this.users.filter((u) => u.username.toLowerCase().includes(q))
    }
  },
  created() {
    this.load()
  },
  methods: {
    formatTime(v) {
      if (!v) return '-'
      const d = new Date(v)
      return isNaN(d.getTime()) ? v : d.toLocaleString()
    },
    async load() {
      this.loading = true
      try {
        const res = await api.users.list()
        this.users = res.items || []
      } catch (e) {
        this.$message.error(e.message || '加载用户失败')
      } finally {
        this.loading = false
      }
    },
    openCreate() {
      this.createForm = { username: '', password: '', confirm: '' }
      this.createVisible = true
    },
    resetCreate() {
      this.createForm = { username: '', password: '', confirm: '' }
    },
    async submitCreate() {
      const f = this.createForm
      const username = (f.username || '').trim()
      if (!/^[A-Za-z0-9_.-]{3,32}$/.test(username)) {
        this.$message.warning('用户名需为 3-32 位字母、数字、下划线、点或短横线')
        return
      }
      if (!f.password || f.password.length < 6) {
        this.$message.warning('密码至少 6 位')
        return
      }
      if (f.password !== f.confirm) {
        this.$message.warning('两次输入的密码不一致')
        return
      }
      this.saving = true
      try {
        await api.users.create(username, f.password)
        this.$message.success('子用户已创建')
        this.createVisible = false
        await this.load()
      } catch (e) {
        this.$message.error(e.message || '创建失败')
      } finally {
        this.saving = false
      }
    },
    openReset(row) {
      this.resetTarget = row
      this.resetForm = { password: '', confirm: '' }
      this.resetVisible = true
    },
    resetReset() {
      this.resetForm = { password: '', confirm: '' }
    },
    async submitReset() {
      const f = this.resetForm
      if (!f.password || f.password.length < 6) {
        this.$message.warning('新密码至少 6 位')
        return
      }
      if (f.password !== f.confirm) {
        this.$message.warning('两次输入的密码不一致')
        return
      }
      this.saving = true
      try {
        await api.users.resetPassword(this.resetTarget.id, f.password)
        this.$message.success('密码已重置，该用户的旧会话和 API 令牌已失效')
        this.resetVisible = false
      } catch (e) {
        this.$message.error(e.message || '重置失败')
      } finally {
        this.saving = false
      }
    },
    openTokens(row) {
      this.tokenTarget = row
      this.tokens = []
      this.tokensVisible = true
      this.loadTokens(row.id)
    },
    resetTokens() {
      this.tokens = []
      this.tokenTarget = {}
    },
    async loadTokens(userId) {
      this.tokensLoading = true
      try {
        const res = await api.tokens.list(userId)
        this.tokens = res.items || []
      } catch (e) {
        this.$message.error(e.message || '加载令牌失败')
      } finally {
        this.tokensLoading = false
      }
    },
    async revokeToken(row) {
      try {
        await this.$confirm(`确认吊销「${row.name}」？使用它的脚本会立即失效。`, '吊销令牌', {
          type: 'warning'
        })
      } catch (e) {
        return
      }
      try {
        await api.tokens.remove(row.id)
        this.$message.success('令牌已吊销')
        await this.loadTokens(this.tokenTarget.id)
      } catch (e) {
        this.$message.error(e.message || '吊销失败')
      }
    },
    async removeUser(row) {
      try {
        await this.$confirm(`确认删除子用户「${row.username}」？`, '删除用户', {
          type: 'warning'
        })
      } catch (e) {
        return
      }
      try {
        await api.users.remove(row.id)
        this.$message.success('用户已删除')
        await this.load()
      } catch (e) {
        this.$message.error(e.message || '删除失败')
      }
    }
  }
}
</script>
