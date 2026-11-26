<template>
  <div class="login-page">
    <div class="login-card">
      <div class="login-brand">
        <div class="login-mark">W</div>
        <div>
          <div class="login-title">集中运维平台</div>
        </div>
      </div>

      <el-input
        ref="username"
        v-model="username"
        placeholder="用户名"
        class="login-input"
        autocomplete="username"
        @keyup.enter.native="focusPassword"
      />

      <el-input
        ref="password"
        v-model="password"
        type="password"
        show-password
        placeholder="密码"
        class="login-input"
        autocomplete="current-password"
        @keyup.enter.native="submit"
      />

      <el-button
        type="primary"
        class="login-btn"
        :loading="loading"
        :disabled="loading"
        @click="submit"
      >
        进入平台
      </el-button>

      <div v-if="error" class="login-error">
        <i class="el-icon-warning-outline"></i><span>{{ error }}</span>
      </div>

      <div class="login-note">
        账号由管理员创建；子用户只能看到自己名下的服务器。仅限受控内网访问。
      </div>
    </div>
  </div>
</template>

<script>
import api from '@/api'

export default {
  name: 'Login',
  data() {
    return {
      username: '',
      password: '',
      loading: false,
      error: ''
    }
  },
  mounted() {
    this.$nextTick(() => {
      const el = this.$refs.username && this.$refs.username.$el.querySelector('input')
      if (el) el.focus()
    })
  },
  methods: {
    focusPassword() {
      const el = this.$refs.password && this.$refs.password.$el.querySelector('input')
      if (el) el.focus()
    },
    async submit() {
      if (!this.username || !this.password) {
        this.error = '请输入用户名和密码'
        return
      }
      this.loading = true
      this.error = ''
      try {
        const res = await api.auth.login(this.username.trim(), this.password)
        this.$store.commit('setUser', (res && res.user) || null)
        const redirect = this.$route.query.redirect || '/dashboard'
        this.password = ''
        this.$router.replace(redirect)
      } catch (e) {
        this.error = e.message || '登录失败'
      } finally {
        this.loading = false
      }
    }
  }
}
</script>

<style scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--body-bg);
  padding: 24px;
}

.login-card {
  position: relative;
  width: 100%;
  max-width: 360px;
  padding: 30px 28px 22px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--panel-highlight);
  overflow: hidden;
}

/* 与顶部控制条同一段铜色导轨，登录页是它的延申 */
.login-card::before {
  content: '';
  position: absolute;
  top: 0; left: 0;
  width: 76px; height: 2px;
  background: var(--accent);
}

.login-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 24px;
}

.login-mark {
  width: 36px;
  height: 36px;
  border-radius: var(--radius);
  background: var(--panel-2);
  border: 1px solid var(--accent);
  color: var(--accent);
  font-family: var(--font-mono);
  font-weight: 600;
  font-size: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.login-title {
  font-size: 15.5px;
  font-weight: 600;
  color: var(--text-strong);
}

.login-sub {
  margin-top: 3px;
  font-family: var(--font-mono);
  font-size: 10.5px;
  letter-spacing: 0.06em;
  color: var(--muted);
}

.login-input {
  margin-bottom: 14px;
}

.login-btn {
  width: 100%;
  height: 36px;
  letter-spacing: 0.02em;
}

.login-error {
  margin-top: 12px;
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--danger);
  font-size: 13px;
}

.login-note {
  margin-top: 20px;
  padding-top: 12px;
  border-top: 1px solid var(--border-soft);
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.6;
}
</style>
