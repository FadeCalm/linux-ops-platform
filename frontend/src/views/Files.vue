<template>
  <div>
    <div class="panel">
      <div class="toolbar">
        <el-select v-model="serverId" placeholder="选择服务器" style="width: 230px" @change="onServerChange">
          <el-option
            v-for="s in servers"
            :key="s.id"
            :value="s.id"
            :label="`${s.name} (${s.ip}) · ${statusText(s.status)}`"
          />
        </el-select>
        <el-input
          v-model="pathInput"
          class="path-input"
          placeholder="/var/log"
          @keyup.enter.native="goPath"
        >
          <template slot="prepend">
            <el-button icon="el-icon-back" :disabled="!parent" @click="enter(parent)" />
          </template>
          <template slot="append">
            <el-button icon="el-icon-position" @click="goPath">前往</el-button>
          </template>
        </el-input>
        <el-button icon="el-icon-refresh" :loading="loading" @click="load" />
        <div class="spacer"></div>
        <el-checkbox v-model="showHidden">显示隐藏文件</el-checkbox>
        <el-button icon="el-icon-folder-add" :disabled="!serverId" @click="openCreate('dir')">新建目录</el-button>
        <el-button icon="el-icon-document-add" :disabled="!serverId" @click="openCreate('file')">新建文件</el-button>
        <el-button type="primary" icon="el-icon-upload2" :disabled="!serverId" @click="$refs.fileInput.click()">
          上传
        </el-button>
        <input ref="fileInput" type="file" multiple style="display: none" @change="onFilesPicked" />
      </div>

      <div class="crumbs">
        <span class="crumb" @click="enterRoot">{{ rootLabel }}</span>
        <template v-for="(c, i) in crumbs">
          <span :key="'sep' + i" class="crumb-sep">/</span>
          <span :key="'c' + i" class="crumb" @click="enter(c.path)">{{ c.name }}</span>
        </template>
        <span class="crumb-meta">{{ total }} 项</span>
      </div>

      <el-table
        v-loading="loading"
        :data="visibleEntries"
        border
        height="520"
        empty-text="目录为空"
        @row-dblclick="openEntry"
      >
        <el-table-column label="名称" min-width="300">
          <template slot-scope="{ row }">
            <span class="file-name">
              <i :class="iconClass(row)"></i>
              <span :class="{ 'file-link': row.type === 'symlink' }">{{ row.name }}</span>
              <span v-if="row.link_target" class="muted link-target">→ {{ row.link_target }}</span>
            </span>
          </template>
        </el-table-column>
        <el-table-column label="大小" width="110" align="right">
          <template slot-scope="{ row }">
            <span class="mono">{{ row.type === 'dir' ? '-' : fmtSize(row.size) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="修改时间" width="170">
          <template slot-scope="{ row }"><span class="muted">{{ formatTime(row.mtime) }}</span></template>
        </el-table-column>
        <el-table-column label="权限" width="120">
          <template slot-scope="{ row }"><span class="mono">{{ row.mode }}</span></template>
        </el-table-column>
        <el-table-column label="操作" width="300" fixed="right">
          <template slot-scope="{ row }">
            <el-button v-if="row.type === 'dir'" type="text" @click="enter(row.path)">进入</el-button>
            <el-button v-else-if="row.type === 'file'" type="text" @click="openEditor(row)">编辑</el-button>
            <el-button v-if="row.type !== 'dir'" type="text" @click="download(row)">下载</el-button>
            <el-button type="text" @click="rename(row)">重命名</el-button>
            <el-button type="text" style="color: var(--danger)" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <div v-if="uploading" class="upload-bar">
        <span class="upload-name">上传 {{ uploadName }}</span>
        <el-progress :percentage="uploadPercent" :stroke-width="8" style="flex: 1" />
      </div>
    </div>

    <!-- 编辑 -->
    <el-dialog :title="editor.path" :visible.sync="editor.visible" width="82%" top="5vh">
      <div class="editor-meta">
        <span class="mono muted">{{ fmtSize(editor.size) }}</span>
        <span class="muted">保存前会校验文件是否已被其他程序修改</span>
      </div>
      <el-input
        v-model="editor.content"
        type="textarea"
        :rows="26"
        class="editor-area"
        spellcheck="false"
      />
      <span slot="footer">
        <el-button @click="editor.visible = false">取消</el-button>
        <el-button type="primary" :loading="editor.saving" @click="saveFile">保存</el-button>
      </span>
    </el-dialog>

    <!-- 新建 -->
    <el-dialog
      :title="create.type === 'dir' ? '新建目录' : '新建文件'"
      :visible.sync="create.visible"
      width="440px"
    >
      <el-input
        v-model="create.name"
        placeholder="输入名称"
        @keyup.enter.native="doCreate"
      />
      <span slot="footer">
        <el-button @click="create.visible = false">取消</el-button>
        <el-button type="primary" :loading="create.saving" @click="doCreate">创建</el-button>
      </span>
    </el-dialog>
  </div>
</template>

<script>
import api from '@/api'
import { formatTime, statusText, decodeBase64 } from '@/utils/format'

const READ_LIMIT = 2 * 1024 * 1024
const UPLOAD_CHUNK = 4 * 1024 * 1024

function b64Encode(str) {
  const bytes = new TextEncoder().encode(str)
  let bin = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    bin += String.fromCharCode.apply(null, bytes.subarray(i, i + chunk))
  }
  return btoa(bin)
}

function randomId() {
  const bytes = new Uint8Array(16)
  window.crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

export default {
  name: 'Files',
  data() {
    return {
      servers: [],
      serverId: null,
      path: '/',
      pathInput: '/',
      parent: '',
      entries: [],
      total: 0,
      loading: false,
      showHidden: false,
      uploading: false,
      uploadName: '',
      uploadPercent: 0,
      editor: { visible: false, path: '', content: '', size: 0, baseMtime: '', saving: false },
      create: { visible: false, type: 'file', name: '', saving: false }
    }
  },
  computed: {
    visibleEntries() {
      if (this.showHidden) return this.entries
      return this.entries.filter((e) => !e.name.startsWith('.'))
    },
    hasDrive() {
      return /^[A-Za-z]:/.test(this.path || '')
    },
    rootLabel() {
      return this.hasDrive ? this.path.slice(0, 2) + '/' : '/'
    },
    crumbs() {
      const p = this.path || '/'
      const drive = (p.match(/^[A-Za-z]:/) || [''])[0]
      const parts = p.slice(drive.length).split('/').filter(Boolean)
      const out = []
      let acc = drive
      parts.forEach((part) => {
        acc += '/' + part
        out.push({ name: part, path: acc })
      })
      return out
    }
  },
  created() {
    this.serverId = Number(this.$route.query.server) || null
    this.path = this.$route.query.path || '/'
    this.pathInput = this.path
    this.loadServers()
  },
  methods: {
    statusText,
    formatTime,
    async loadServers() {
      try {
        const res = await api.servers.list({ page: 1, page_size: 200 })
        this.servers = res.items || []
        if (!this.serverId && this.servers.length) {
          const online = this.servers.find((s) => s.status === 'online')
          this.serverId = (online || this.servers[0]).id
        }
        if (this.serverId) this.load()
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    onServerChange() {
      this.load()
    },
    async load() {
      if (!this.serverId) return
      this.loading = true
      try {
        const res = await api.fs.list(this.serverId, this.path, { offset: 0, limit: 1000 })
        this.entries = res.entries || []
        this.total = res.total || 0
        this.path = res.path
        this.pathInput = res.path
        this.parent = res.parent || ''
        this.syncQuery()
      } catch (e) {
        this.entries = []
        this.total = 0
        this.$message.error(e.message)
      } finally {
        this.loading = false
      }
    },
    syncQuery() {
      this.$router
        .replace({ path: '/files', query: { server: this.serverId, path: this.path } })
        .catch(() => {})
    },
    enter(p) {
      if (!p) return
      this.path = p
      this.load()
    },
    enterRoot() {
      this.enter(this.hasDrive ? this.path.slice(0, 2) + '/' : '/')
    },
    goPath() {
      const p = (this.pathInput || '').trim()
      if (!p) return
      this.enter(p)
    },
    openEntry(row) {
      if (row.type === 'dir') this.enter(row.path)
      else if (row.type === 'file') this.openEditor(row)
    },
    iconClass(row) {
      if (row.type === 'dir') return 'el-icon-folder'
      if (row.type === 'symlink') return 'el-icon-link'
      return 'el-icon-document'
    },
    fmtSize(n) {
      if (n === null || n === undefined) return '-'
      if (n < 1024) return n + ' B'
      if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB'
      if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB'
      return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB'
    },
    joinPath(dir, name) {
      return dir.endsWith('/') ? dir + name : dir + '/' + name
    },
    dirOf(p) {
      const i = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'))
      if (i < 0) return p
      return i === 0 ? '/' : p.slice(0, i)
    },
    async openEditor(row) {
      try {
        const res = await api.fs.read(this.serverId, row.path, 0, READ_LIMIT)
        if (res.binary) {
          this.$message.warning('二进制文件无法在线编辑，请下载后处理')
          return
        }
        if (res.size > READ_LIMIT) {
          this.$message.warning('文件超过 2 MB，暂不支持在线编辑，请下载后处理')
          return
        }
        this.editor = {
          visible: true,
          path: res.path,
          content: decodeBase64(res.data),
          size: res.size,
          baseMtime: res.mod_time,
          saving: false
        }
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async saveFile() {
      this.editor.saving = true
      try {
        await api.fs.write(this.serverId, {
          path: this.editor.path,
          data: b64Encode(this.editor.content),
          base_mtime: this.editor.baseMtime
        })
        this.$message.success('已保存')
        this.editor.visible = false
        this.load()
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.editor.saving = false
      }
    },
    download(row) {
      const a = document.createElement('a')
      a.href = api.fs.downloadUrl(this.serverId, row.path)
      a.download = row.name
      document.body.appendChild(a)
      a.click()
      a.remove()
    },
    async rename(row) {
      let name
      try {
        const res = await this.$prompt('新的名称', '重命名', {
          inputValue: row.name,
          inputValidator: (v) => (!v || v.includes('/') ? '名称不能包含 /' : true)
        })
        name = res.value.trim()
      } catch (e) {
        return
      }
      try {
        await api.fs.op(this.serverId, {
          op: 'rename',
          path: row.path,
          new_path: this.joinPath(this.dirOf(row.path), name)
        })
        this.$message.success('已重命名')
        this.load()
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    async remove(row) {
      try {
        await this.$confirm(
          row.type === 'dir'
            ? `确定递归删除目录 ${row.name} 及其全部内容？`
            : `确定删除文件 ${row.name}？`,
          '删除确认',
          { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
        )
      } catch (e) {
        return
      }
      try {
        await api.fs.op(this.serverId, { op: 'delete', path: row.path })
        this.$message.success('已删除')
        this.load()
      } catch (e) {
        this.$message.error(e.message)
      }
    },
    openCreate(type) {
      this.create = { visible: true, type, name: '', saving: false }
    },
    async doCreate() {
      const name = (this.create.name || '').trim()
      if (!name) {
        this.$message.warning('请输入名称')
        return
      }
      if (name.includes('/')) {
        this.$message.warning('名称不能包含 /')
        return
      }
      this.create.saving = true
      try {
        await api.fs.op(this.serverId, {
          op: this.create.type === 'dir' ? 'mkdir' : 'touch',
          path: this.joinPath(this.path, name)
        })
        this.$message.success('已创建')
        this.create.visible = false
        this.load()
      } catch (e) {
        this.$message.error(e.message)
      } finally {
        this.create.saving = false
      }
    },
    onFilesPicked(event) {
      const files = Array.from(event.target.files || [])
      event.target.value = ''
      files.reduce((p, file) => p.then(() => this.uploadFile(file)), Promise.resolve())
    },
    async uploadFile(file) {
      const target = this.joinPath(this.path, file.name)
      this.uploading = true
      this.uploadName = file.name
      this.uploadPercent = 0
      try {
        if (file.size === 0) {
          await api.fs.op(this.serverId, { op: 'touch', path: target })
          this.$message.success(`${file.name} 上传完成`)
          return
        }
        const uploadId = randomId()
        for (let offset = 0; offset < file.size; offset += UPLOAD_CHUNK) {
          const blob = file.slice(offset, Math.min(offset + UPLOAD_CHUNK, file.size))
          const res = await api.fs.upload(
            this.serverId,
            { path: target, upload_id: uploadId, offset, total: file.size, mode: '0644' },
            blob,
            (evt) => {
              const loaded = offset + (evt.loaded || 0)
              this.uploadPercent = Math.min(100, Math.round((loaded / file.size) * 100))
            }
          )
          if (res.received !== offset + blob.size) {
            throw new Error('上传进度不一致，请重试')
          }
        }
        this.uploadPercent = 100
        this.$message.success(`${file.name} 上传完成`)
        this.load()
      } catch (e) {
        this.$message.error(`${file.name} 上传失败：${e.message}`)
      } finally {
        this.uploading = false
        this.uploadPercent = 0
      }
    }
  }
}
</script>

<style scoped>
.path-input {
  flex: 1;
  min-width: 320px;
  max-width: 620px;
}
.crumbs {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px;
  margin: -2px 0 12px;
  font-family: Consolas, monospace;
  font-size: 12.5px;
  color: var(--muted);
}
.crumb {
  cursor: pointer;
  padding: 2px 5px;
  border-radius: 5px;
  color: var(--text);
}
.crumb:hover {
  color: var(--accent);
  background: var(--accent-soft);
}
.crumb-sep {
  color: var(--muted);
  opacity: 0.6;
}
.crumb-meta {
  margin-left: 12px;
  font-family: inherit;
  color: var(--muted);
}
.file-name {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
}
.file-name i {
  color: var(--accent);
  font-size: 15px;
}
.file-link {
  color: var(--accent-2);
}
.link-target {
  font-size: 11.5px;
}
.upload-bar {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-top: 12px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--panel-2);
}
.upload-name {
  min-width: 180px;
  color: var(--text);
}
.editor-meta {
  display: flex;
  gap: 16px;
  margin-bottom: 10px;
  font-size: 12px;
}
.editor-area >>> textarea {
  font-family: 'JetBrains Mono', Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.55;
}
</style>
