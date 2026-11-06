<template>
  <div class="login-page">
    <div class="login-card">
      <div class="login-brand">
        <div class="login-mark">L</div>
        <div>
          <div class="login-title">集中运维平台</div>
          <div class="login-sub">输入访问密码后进入控制台</div>
        </div>
      </div>

      <el-input
        ref="password"
        v-model="password"
        type="password"
        show-password
        placeholder="访问密码"
        class="login-input"
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
        本平台运行在隔离内网，密码为统一访问口令，不区分用户。
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
      password: '',
      loading: false,
      error: ''
    }
  },
  mounted() {
    this.$nextTick(() => {
      const el = this.$refs.password && this.$refs.password.$el.querySelector('input')
      if (el) el.focus()
    })
  },
  methods: {
    async submit() {
      if (!this.password) {
        this.error = '请输入访问密码'
        return
      }
      this.loading = true
      this.error = ''
      try {
        await api.auth.login(this.password)
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
  width: 100%;
  max-width: 380px;
  padding: 32px 30px 24px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 14px;
  box-shadow: 0 24px 60px rgba(0, 0, 0, 0.45);
}

.login-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 26px;
}

.login-mark {
  width: 40px;
  height: 40px;
  border-radius: 10px;
  background: var(--accent);
  color: var(--on-accent);
  font-weight: 800;
  font-size: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.login-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--text-strong);
}

.login-sub {
  margin-top: 3px;
  font-size: 12px;
  color: var(--muted);
}

.login-input {
  margin-bottom: 14px;
}

.login-btn {
  width: 100%;
  height: 38px;
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
  margin-top: 22px;
  padding-top: 14px;
  border-top: 1px solid var(--border-soft);
  color: var(--placeholder);
  font-size: 12px;
  line-height: 1.6;
}
</style>
