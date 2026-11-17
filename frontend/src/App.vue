<template>
  <div v-if="!isPublicRoute" class="app-shell">
    <header class="topbar">
      <div class="brand">
        <div class="brand-mark">L</div>
        <div class="brand-text">
          <span class="brand-title">集中运维</span>
          <span class="brand-sub">wxgzt</span>
        </div>
      </div>
      <nav class="top-nav">
        <router-link
          v-for="item in nav"
          :key="item.path"
          :to="item.path"
          class="top-nav-item"
          active-class="active"
        >
          <i :class="item.icon"></i><span>{{ item.title }}</span>
        </router-link>
      </nav>
      <div class="top-status">
        <span class="status-dot" :class="{ online: backendOnline }"></span>
        <span>{{ backendOnline ? '后端在线' : '后端离线' }}</span>
        <el-tooltip :content="theme === 'dark' ? '切换到浅色（白）配色' : '切换到深色（黑）配色'" placement="bottom">
          <button class="theme-toggle" type="button" @click="toggleTheme">
            <i :class="theme === 'dark' ? 'el-icon-sunny' : 'el-icon-moon'"></i>
            <span>{{ theme === 'dark' ? '浅色' : '深色' }}</span>
          </button>
        </el-tooltip>
        <span v-if="user" class="top-user">
          {{ user.username }}
          <em>{{ user.role === 'admin' ? '管理员' : '子用户' }}</em>
        </span>
        <el-tooltip v-if="user" content="修改自己的密码" placement="bottom">
          <button class="theme-toggle" type="button" @click="openPassword">
            <i class="el-icon-key"></i>
            <span>密码</span>
          </button>
        </el-tooltip>
        <el-tooltip v-if="user" content="为脚本创建 API 令牌（Authorization: Bearer）" placement="bottom">
          <button class="theme-toggle" type="button" @click="openTokens">
            <i class="el-icon-tickets"></i>
            <span>令牌</span>
          </button>
        </el-tooltip>
        <el-tooltip v-if="authRequired" content="退出登录" placement="bottom">
          <button class="theme-toggle" type="button" @click="logout">
            <i class="el-icon-switch-button"></i>
            <span>退出</span>
          </button>
        </el-tooltip>
      </div>
    </header>

    <div class="body">
      <aside class="sidebar">
        <div class="side-section">导航</div>
        <router-link
          v-for="item in nav"
          :key="item.path"
          :to="item.path"
          class="side-item"
          active-class="active"
        >
          <i :class="item.icon"></i><span>{{ item.title }}</span>
        </router-link>

        <div class="side-section" style="margin-top: 24px">环境提示</div>
        <div class="side-note">
          <div class="side-note-title"><i class="el-icon-warning-outline"></i> 隔离网络</div>
          <div class="side-note-text">
            子用户只能看到自己名下的服务器；管理员可见全部。仅限受控内网，禁止暴露公网。
          </div>
        </div>
      </aside>

      <main class="content">
        <div class="page-head">
          <h2>{{ $route.meta.title }}</h2>
          <div class="page-sub">{{ subtitle }}</div>
        </div>
        <router-view />
      </main>
    </div>

    <el-dialog title="修改密码" :visible.sync="pwdVisible" width="420px" @closed="resetPasswordForm">
      <el-form label-width="90px" @submit.native.prevent>
        <el-form-item label="原密码">
          <el-input v-model="pwd.old" type="password" show-password autocomplete="current-password" />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="pwd.next" type="password" show-password autocomplete="new-password" placeholder="至少 6 位" />
        </el-form-item>
        <el-form-item label="确认新密码">
          <el-input v-model="pwd.confirm" type="password" show-password autocomplete="new-password" />
        </el-form-item>
      </el-form>
      <div slot="footer">
        <el-button @click="pwdVisible = false">取消</el-button>
        <el-button type="primary" :loading="pwdSaving" @click="submitPassword">保存</el-button>
      </div>
    </el-dialog>

    <el-dialog title="API 令牌" :visible.sync="tokensVisible" width="760px" @closed="resetTokens">
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        title="令牌等同于你的账号密码，请只放在受控服务器上；明文只在创建时显示一次，请立即保存。请求头：Authorization: Bearer <token>"
        style="margin-bottom: 14px"
      />

      <div v-if="newToken" class="token-box">
        <div class="token-label">新令牌（仅显示这一次）</div>
        <div class="token-row">
          <code class="token-value">{{ newToken }}</code>
          <el-button size="mini" icon="el-icon-document-copy" @click="copyToken">复制</el-button>
        </div>
      </div>

      <div class="token-create">
        <el-input
          v-model="tokenForm.name"
          placeholder="令牌名称，例如 backup-script"
          maxlength="64"
          style="width: 260px"
        />
        <el-select v-model="tokenForm.days" style="width: 140px">
          <el-option
            v-for="o in expiryOptions"
            :key="o.value"
            :label="o.label"
            :value="o.value"
          />
        </el-select>
        <el-button type="primary" icon="el-icon-plus" :loading="tokenCreating" @click="createToken">
          生成令牌
        </el-button>
      </div>

      <el-table v-loading="tokensLoading" :data="tokens" border empty-text="暂无令牌">
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
  <router-view v-else />
</template>

<script>
import api from '@/api'
import { applyTheme, getTheme } from '@/utils/theme'

export default {
  name: 'App',
  data() {
    return {
      theme: getTheme(),
      pwdVisible: false,
      pwdSaving: false,
      pwd: { old: '', next: '', confirm: '' },
      tokensVisible: false,
      tokensLoading: false,
      tokenCreating: false,
      tokens: [],
      newToken: '',
      tokenForm: { name: '', days: 90 },
      expiryOptions: [
        { value: 30, label: '30 天' },
        { value: 90, label: '90 天' },
        { value: 365, label: '365 天' },
        { value: 0, label: '永不过期' }
      ]
    }
  },
  computed: {
    isPublicRoute() {
      return !!(this.$route.meta && this.$route.meta.public)
    },
    authRequired() {
      return this.$store.state.authRequired
    },
    backendOnline() {
      return this.$store.state.backendOnline
    },
    user() {
      return this.$store.state.user
    },
    isAdmin() {
      return this.$store.getters.isAdmin
    },
    nav() {
      const base = [
        { path: '/dashboard', title: 'System info', icon: 'el-icon-data-line' },
        { path: '/servers', title: 'Resource', icon: 'el-icon-monitor' },
        { path: '/execute', title: 'Command', icon: 'el-icon-cpu' },
        { path: '/files', title: 'File', icon: 'el-icon-folder-opened' },
        { path: '/terminal', title: 'Batch', icon: 'el-icon-connection' },
        { path: '/history', title: 'History', icon: 'el-icon-document' }
      ]
      if (this.isAdmin) {
        base.push({ path: '/users', title: 'Users', icon: 'el-icon-user' })
      }
      return base
    },
    subtitle() {
      const map = {
        dashboard: '服务器在线状态',
        servers: '健康状态',
        execute: 'r',
        files: '',
        history: '',
        'server-execute': '',
        terminal: '',
        'terminal-pick': '选择一台受管主机，打开真实的交互式 Shell 会话',
        users: '管理员创建子用户、重置密码；子用户只能看到自己名下的服务器'
      }
      return map[this.$route.name] || ''
    }
  },
  methods: {
    toggleTheme() {
      this.theme = this.theme === 'dark' ? 'light' : 'dark'
      applyTheme(this.theme)
    },
    refreshLimits() {
      this.$store.dispatch('loadLimits').catch(() => {})
    },
    openPassword() {
      this.pwd = { old: '', next: '', confirm: '' }
      this.pwdVisible = true
    },
    resetPasswordForm() {
      this.pwd = { old: '', next: '', confirm: '' }
    },
    async submitPassword() {
      if (!this.pwd.old || !this.pwd.next) {
        this.$message.warning('请填写原密码和新密码')
        return
      }
      if (this.pwd.next.length < 6) {
        this.$message.warning('新密码至少 6 位')
        return
      }
      if (this.pwd.next !== this.pwd.confirm) {
        this.$message.warning('两次输入的新密码不一致')
        return
      }
      this.pwdSaving = true
      try {
        await api.auth.changePassword(this.pwd.old, this.pwd.next)
        this.pwdVisible = false
        this.$message.success('密码已修改，其他设备上的登录已失效')
      } catch (e) {
        this.$message.error(e.message || '修改失败')
      } finally {
        this.pwdSaving = false
      }
    },
    async logout() {
      try {
        await api.auth.logout()
      } catch (e) {
        // Session already invalid; still go back to the login page.
      }
      this.$store.commit('setUser', null)
      this.$router.replace('/login')
    },
    formatTime(v) {
      if (!v) return '-'
      const d = new Date(v)
      return isNaN(d.getTime()) ? v : d.toLocaleString()
    },
    openTokens() {
      this.newToken = ''
      this.tokenForm = { name: '', days: 90 }
      this.tokensVisible = true
      this.loadTokens()
    },
    resetTokens() {
      this.tokens = []
      this.newToken = ''
    },
    async loadTokens() {
      this.tokensLoading = true
      try {
        const res = await api.tokens.list()
        this.tokens = res.items || []
      } catch (e) {
        this.$message.error(e.message || '加载令牌失败')
      } finally {
        this.tokensLoading = false
      }
    },
    async createToken() {
      this.tokenCreating = true
      try {
        const res = await api.tokens.create((this.tokenForm.name || '').trim(), this.tokenForm.days)
        this.newToken = res.token
        this.tokenForm = { name: '', days: this.tokenForm.days }
        this.$message.success('令牌已生成，请立即复制保存')
        await this.loadTokens()
      } catch (e) {
        this.$message.error(e.message || '生成失败')
      } finally {
        this.tokenCreating = false
      }
    },
    async revokeToken(row) {
      try {
        await this.$confirm(`确认吊销令牌「${row.name}」？使用它的脚本会立即失效。`, '吊销令牌', {
          type: 'warning'
        })
      } catch (e) {
        return
      }
      try {
        await api.tokens.remove(row.id)
        this.$message.success('令牌已吊销')
        await this.loadTokens()
      } catch (e) {
        this.$message.error(e.message || '吊销失败')
      }
    },
    copyToken() {
      const text = this.newToken
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard
          .writeText(text)
          .then(() => this.$message.success('已复制'))
          .catch(() => this.fallbackCopy(text))
        return
      }
      this.fallbackCopy(text)
    },
    fallbackCopy(text) {
      const ta = document.createElement('textarea')
      ta.value = text
      document.body.appendChild(ta)
      ta.select()
      try {
        document.execCommand('copy')
        this.$message.success('已复制')
      } catch (e) {
        this.$message.warning('复制失败，请手动选择复制')
      }
      document.body.removeChild(ta)
    }
  },
  watch: {
    isPublicRoute(publicRoute) {
      if (!publicRoute) this.refreshLimits()
    }
  },
  created() {
    if (!this.isPublicRoute) this.refreshLimits()
    this.timer = setInterval(() => {
      if (!this.isPublicRoute) this.refreshLimits()
    }, 15000)
  },
  beforeDestroy() {
    clearInterval(this.timer)
  }
}
</script>

<style scoped>
.top-user {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
}
.top-user em {
  font-style: normal;
  padding: 1px 6px;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-strong);
}
.token-box {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 12px;
  margin-bottom: 12px;
}
.token-label {
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 6px;
}
.token-row {
  display: flex;
  align-items: center;
  gap: 10px;
}
.token-value {
  flex: 1;
  word-break: break-all;
  padding: 6px 8px;
  border-radius: 6px;
  background: rgba(127, 127, 127, 0.12);
  font-size: 12px;
}
.token-create {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 14px;
}
</style>
