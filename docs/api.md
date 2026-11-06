# API 参考（M1–M3）

本平台有两套 HTTP API：

- **管理后端**：浏览器与 curl 使用，前缀 `/api/v1`
- **Agent**：仅由后端调用（也可用于排障），前缀 `/api/v1`，默认端口 `19090`

> 第一阶段没有认证。请勿在公网暴露任何端口。所有示例都假设本机访问。

## 1. 通用约定

### 1.1 后端响应信封

成功：

```json
{ "code": 0, "data": { } }
```

失败（HTTP 状态码同时非 2xx）：

```json
{ "code": 400, "message": "command is required" }
```

### 1.2 分页

列表接口接受：

| 参数 | 默认 | 约束 |
|---|---|---|
| `page` | 1 | ≥ 1 |
| `page_size` | 20 | 1–200 |

返回：

```json
{ "items": [], "total": 0, "page": 1, "page_size": 20 }
```

### 1.3 时间

所有时间以 RFC 3339 UTC 返回，前端负责本地化。任务筛选参数
`from`/`to` 也使用 RFC 3339。

### 1.4 限制

`GET /api/v1/config/limits` 返回当前生效值：

```json
{
  "code": 0,
  "data": {
    "dispatch_concurrency": 8,
    "default_timeout_ms": 300000,
    "max_timeout_ms": 3600000,
    "max_script_bytes": 65536,
    "max_output_bytes": 10485760,
    "poll_interval_ms": 300,
    "heartbeat_sec": 20,
    "ws_max_connections": 200
  }
}
```

请求体上限约为 `max_script_bytes * 2 + 1 MiB`。

---

## 2. 后端 API

### 2.1 健康与概览

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/health` | 存活检查 |
| GET | `/api/v1/config/limits` | 只读限制 |
| GET | `/api/v1/stats/overview` | 仪表盘统计 |

`GET /api/v1/stats/overview`：

```json
{
  "code": 0,
  "data": {
    "servers_total": 3,
    "servers_online": 2,
    "servers_offline": 1,
    "agent_versions": [{ "version": "0.1.0", "count": 2 }],
    "recent_tasks": [],
    "tasks_total": 42,
    "tasks_success": 40,
    "success_rate": 95.24,
    "ws_connections": 1
  }
}
```

### 2.2 服务器

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/servers` | 列表，支持筛选 |
| POST | `/api/v1/servers` | 新增 |
| GET | `/api/v1/servers/{id}` | 详情（含 tags） |
| PUT | `/api/v1/servers/{id}` | 更新 |
| DELETE | `/api/v1/servers/{id}` | 删除（有活跃任务时 409） |
| POST | `/api/v1/servers/{id}/check` | 即时健康检查 |
| GET | `/api/v1/servers/{id}/agent` | Agent 上报信息 |

列表查询参数：

| 参数 | 说明 |
|---|---|
| `q` | 模糊匹配 name / ip / hostname |
| `status` | `online` / `offline` / `unknown` |
| `group_id` | 分组 ID |
| `tag` | 标签名 |
| `page`, `page_size` | 分页 |

新增/更新请求体：

```json
{
  "name": "web-01",
  "ip": "10.0.0.11",
  "agent_port": 19090,
  "group_id": 1,
  "tags": ["web", "prod"]
}
```

- `name`、`ip` 必填；`agent_port` 默认 `19090`
- `tags` 中的名字不存在时会自动创建
- 新增服务器的初始 `status` 为 `unknown`

健康检查响应：

```json
{ "code": 0, "data": { "online": true, "info": {
  "hostname": "web-01", "os": "linux", "arch": "amd64",
  "version": "0.1.0", "uptime_sec": 120, "running_tasks": 0, "max_concurrent": 8
} } }
```

Agent 不可达时仍返回 200：`{"online": false, "error": "..."}`。

### 2.3 分组与标签

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/groups` | 列出全部分组（不分页） |
| POST | `/api/v1/groups` | 新建 `{name, description}` |
| PUT | `/api/v1/groups/{id}` | 更新 `{name, description}` |
| DELETE | `/api/v1/groups/{id}` | 删除；相关服务器的 `group_id` 置空 |
| GET | `/api/v1/tags` | 列出全部标签（不分页） |
| POST | `/api/v1/tags` | 新建 `{name}` |
| DELETE | `/api/v1/tags/{id}` | 删除并清理关联 |

### 2.4 任务

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/tasks` | 创建批量任务 |
| GET | `/api/v1/tasks` | 任务历史（分页） |
| GET | `/api/v1/tasks/{id}` | 父任务 + 全部子任务 |
| GET | `/api/v1/tasks/{id}/targets` | 仅子任务 |
| POST | `/api/v1/tasks/{id}/cancel` | 取消该任务所有活跃子任务 |
| GET | `/api/v1/targets/{id}` | 单个子任务 |
| GET | `/api/v1/targets/{id}/logs` | 读日志分片 |
| GET | `/api/v1/targets/{id}/logs/download` | 下载完整日志文件 |
| POST | `/api/v1/targets/{id}/cancel` | 取消单个子任务 |
| POST | `/api/v1/targets/{id}/retry` | 以新任务重试单台 |

#### 创建任务

```http
POST /api/v1/tasks
Content-Type: application/json

{
  "command": "uname -a && df -h",
  "work_dir": "/tmp",
  "timeout_ms": 30000,
  "server_ids": [1, 2],
  "idempotency_key": "deploy-2026-11-06-web"
}
```

字段：

| 字段 | 必填 | 说明 |
|---|---|---|
| `command` | 是 | 交给 `/bin/bash -c` 的脚本；受 `max_script_bytes` 限制 |
| `work_dir` | 否 | 工作目录；空则使用 Agent 默认 |
| `timeout_ms` | 否 | 默认 `default_timeout_ms`，超过 `max_timeout_ms` 会被截断 |
| `server_ids` | 是 | 至少一个；重复 ID 自动去重 |
| `idempotency_key` | 否 | 唯一键；已存在时直接返回原任务 |

响应 200：

```json
{
  "code": 0,
  "data": {
    "task": {
      "id": "6f0a...", "command": "uname -a && df -h", "work_dir": "/tmp",
      "timeout_ms": 30000, "status": "PENDING", "total": 2,
      "succeeded": 0, "failed": 0, "cancelled": 0,
      "created_by": "anonymous", "created_at": "2026-11-06T10:00:00Z",
      "finished_at": null
    },
    "targets": [
      {
        "id": "3b7c...", "parent_task_id": "6f0a...", "server_id": 1,
        "server_name": "web-01", "agent_addr": "10.0.0.11:19090",
        "status": "PENDING", "exit_code": null, "duration_ms": 0,
        "error": "", "stdout_offset": 0, "stderr_offset": 0, "seq": 0,
        "started_at": null, "finished_at": null, "created_at": "..."
      }
    ]
  }
}
```

任务历史查询参数：

| 参数 | 说明 |
|---|---|
| `status` | 父任务状态 |
| `server_id` | 包含该服务器子任务的任务 |
| `from`, `to` | RFC 3339 时间范围 |
| `page`, `page_size` | 分页 |

#### 读日志分片

```http
GET /api/v1/targets/{id}/logs?stream=stdout&offset=0&limit=32768
```

| 参数 | 默认 | 说明 |
|---|---|---|
| `stream` | `stdout` | `stdout` 或 `stderr` |
| `offset` | 0 | 字节偏移 |
| `limit` | 32768 | 1–1048576 |

响应：

```json
{
  "code": 0,
  "data": {
    "stream": "stdout",
    "offset": 0,
    "next_offset": 27,
    "data": "aGVsbG8K",
    "eof": true,
    "seq": 42
  }
}
```

`data` 为 base64。`seq` 是该目标当前的广播游标（在读取文件之后取得），
前端可用它作为 WebSocket 重连的 `resume` 值，避免把已读内容重复追加。

#### 下载

```http
GET /api/v1/targets/{id}/logs/download?stream=stderr
```

直接返回 `application/octet-stream` 附件。

#### 取消与重试

```json
POST /api/v1/targets/{id}/cancel
→ { "code": 0, "data": { "cancelling": true } }

POST /api/v1/tasks/{id}/cancel
→ { "code": 0, "data": { "cancelled": 2 } }

POST /api/v1/targets/{id}/retry
→ { "code": 0, "data": { "task": {...}, "targets": [...] } }
```

- 单目标取消：已结束返回 409
- 批量取消：返回本次进程内确认取消的数量；不在当前进程的目标会被标记为
  `CANCELLED`，避免恢复时再次执行
- 重试会创建一个**新的父任务**（只含该服务器），便于审计与历史对照

### 2.5 审计

```http
GET /api/v1/audit?action=task.create&resource=task&page=1&page_size=20
```

返回 `{items,total,page,page_size}`。`operator` 恒为 `anonymous`。

主要 action：

`task.create`、`task.cancel`、`target.cancel`、`target.retry`、
`server.create/update/delete`、`group.create/update/delete`、
`tag.create/delete`。

---

### 2.6 文件管理

所有路径必须是绝对路径；写操作会记录审计事件（`fs.write` / `fs.upload` /
`fs.mkdir` / `fs.rename` / `fs.delete` / `fs.download`）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/servers/{id}/fs/list?path=/` | 列目录，目录优先排序，返回 `path/parent/total/entries` |
| GET | `/api/v1/servers/{id}/fs/stat?path=` | 单文件信息 + 是否二进制 |
| GET | `/api/v1/servers/{id}/fs/read?path=&offset=&limit=` | 读取文本（base64，上限 2 MiB） |
| PUT | `/api/v1/servers/{id}/fs/write` | 原子保存文件，`{path,data,base_mtime,mode}` |
| GET | `/api/v1/servers/{id}/fs/download?path=` | 流式下载，支持 `Range` / `206` |
| PUT | `/api/v1/servers/{id}/fs/upload?path=&upload_id=&offset=&total=&mode=` | 分片上传，请求体为原始字节 |
| POST | `/api/v1/servers/{id}/fs/op` | `{op:"mkdir"\|"touch"\|"rename"\|"chmod"\|"delete", path, new_path?, mode?}` |

- `write` 带 `base_mtime` 时做乐观并发校验：文件已被修改返回 `409`；
- `upload` 每片上限 32 MiB，`offset` 与临时文件长度不一致返回 `409`，
  `offset + len(body) == total` 时自动 rename 到位；
- `download` 的 `Content-Disposition` 使用 RFC 5987 文件名；
- 目录删除为递归删除，拒绝删除文件系统根。

### 2.7 Agent 在线升级

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/servers/{id}/agent/update/check` | 查询最新版本 |
| POST | `/api/v1/servers/{id}/agent/update` | 触发升级 |
| POST | `/api/v1/agents/update/check` | 批量检查，body `{server_ids:[...]}` |
| POST | `/api/v1/agents/update` | 批量升级，body `{server_ids:[...]}` |

升级流程：Agent 以匿名 `ListObjectsV2` 扫描 `update_base_url` 指向的桶，按
`linux-ops-agent-<version>-linux-<arch>.tar.gz` 取高于自身的最大版本；若
`manifest.json` 存在则一并参考，取两者中较高者，并优先采用 manifest 中的
`notes`/`sha256`。校验和缺失时再查 `SHA256SUMS`；仍缺失则默认跳过校验
（`update_require_checksum: true` 可强制要求）。随后下载 tarball → 校验
SHA256 → 替换自身二进制（旧文件保留为 `agent.old`）→ `exec` 原地重启。

因此发布新版本只需把命名正确的 tar.gz 放进桶里，不必手改 manifest；
桶须允许匿名 `s3:ListBucket` + `s3:GetObject`。`/update/check` 额外返回
`source`（`manifest` / `listing` / `manifest+listing`）与 `verified`。
升级期间 Agent 会短暂不可用；有任务运行时 Agent 返回 `409`。

## 3. WebSocket 协议

### 3.1 连接

```
ws://<backend>/api/v1/ws/tasks/{taskId}
```

任务不存在时返回 HTTP 404；连接数超过 `websocket.max_connections` 时
以 close code `1013`（Try Again Later）关闭。

### 3.2 服务端 → 客户端

连接建立后立即收到 `snapshot`：

```json
{
  "type": "snapshot",
  "task_id": "6f0a...",
  "status": "RUNNING",
  "targets": [
    {
      "target_id": "3b7c...",
      "server_id": 1,
      "server_name": "web-01",
      "status": "RUNNING",
      "seq": 12
    }
  ]
}
```

输出帧：

```json
{
  "type": "stdout",
  "task_id": "6f0a...",
  "target_id": "3b7c...",
  "server_id": 1,
  "server_name": "web-01",
  "seq": 13,
  "ts": "2026-11-06T10:00:01Z",
  "stream": "stdout",
  "data": "aGVsbG8K"
}
```

- `type` 为 `stdout` 或 `stderr`
- `data` 为 base64
- `seq` 对同一 `target_id` 单调递增

状态帧：

```json
{
  "type": "status",
  "task_id": "6f0a...",
  "target_id": "3b7c...",
  "status": "SUCCESS",
  "exit_code": 0,
  "duration_ms": 153,
  "error": "",
  "ts": "..."
}
```

父任务状态：

```json
{ "type": "task_status", "task_id": "6f0a...", "status": "SUCCESS", "ts": "..." }
```

### 3.3 客户端 → 服务端

订阅 / 重连回放：

```json
{
  "type": "subscribe",
  "resume": { "3b7c...": 12 }
}
```

服务端回放该任务下所有目标中 `seq > resume[targetID]` 的缓冲帧。
未列出的目标从 0 开始。

通过 WebSocket 取消：

```json
{ "type": "cancel", "target_id": "3b7c..." }
```

### 3.4 心跳与超时

- 客户端应每 25–30 秒发送 WebSocket ping（前端已实现）
- 服务端读超时 60 秒，收到 pong 后重置
- 环形缓冲只保留最近 `replay_buffer_len` 帧；更早内容请用 REST 日志接口

---

## 4. Agent API

Agent 只被后端调用，也可用 curl 排障。Agent 返回裸 JSON（无 `code/data` 信封）。
错误格式：

```json
{ "error": "task not found" }
```

### 4.1 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/health` | `{"status":"ok","uptime_sec":123}` |
| GET | `/api/v1/info` | hostname/os/arch/version/running_tasks/max_concurrent |
| GET | `/api/v1/version` | `{"version":"0.1.0"}` |
| POST | `/api/v1/tasks` | 提交任务（按 `task_id` 幂等） |
| GET | `/api/v1/tasks/{id}` | 查询状态 |
| GET | `/api/v1/tasks/{id}/logs` | 游标增量日志 |
| POST | `/api/v1/tasks/{id}/cancel` | 取消并杀进程组 |
| GET | `/api/v1/fs/list` `stat` `read` `download` | 浏览 / 读取 / 下载 |
| PUT | `/api/v1/fs/write` `upload` | 原子保存 / 分片上传 |
| POST | `/api/v1/fs/op` | mkdir / touch / rename / chmod / delete |
| GET | `/api/v1/update/check` | 扫描桶（+ manifest）对比当前版本 |
| POST | `/api/v1/update` | 下载、校验并替换自身二进制 |

### 4.2 提交任务

```http
POST /api/v1/tasks
Content-Type: application/json

{
  "task_id": "3b7c...",
  "command": "echo hello; uname -a",
  "work_dir": "/tmp",
  "timeout_ms": 10000
}
```

- `task_id`、`command` 必填
- 请求体使用 `DisallowUnknownFields`，未知字段会被拒绝
- 若 `task_id` 已存在：返回当前状态，**不会重复执行**
- 命令长度受 Agent `max_script_bytes` 限制

成功响应即任务状态：

```json
{
  "task_id": "3b7c...",
  "status": "PENDING",
  "exit_code": null,
  "duration_ms": 0,
  "error": "",
  "finished": false
}
```

`status` 取值：`PENDING`、`RUNNING`、`SUCCESS`、`FAILED`、`TIMEOUT`、`CANCELLED`。

### 4.3 查询任务

```http
GET /api/v1/tasks/{id}
```

不存在返回 404。

### 4.4 拉取日志

```http
GET /api/v1/tasks/{id}/logs?stdout_offset=0&stderr_offset=0&limit=262144
```

| 参数 | 默认 | 说明 |
|---|---|---|
| `stdout_offset` | 0 | stdout 字节游标 |
| `stderr_offset` | 0 | stderr 字节游标 |
| `limit` | 262144 | 每路最多返回字节数，上限 1 MiB |

响应：

```json
{
  "task_id": "3b7c...",
  "status": "RUNNING",
  "exit_code": null,
  "duration_ms": 0,
  "stdout_offset": 12,
  "stderr_offset": 0,
  "stdout": "aGVsbG8K",
  "stderr": "",
  "truncated": false,
  "finished": false,
  "error": ""
}
```

- `stdout` / `stderr` 为 base64
- 返回的 `*_offset` 是**下一次调用**应传的游标
- 超过 `max_output_bytes` 后缓冲截断，`truncated=true`
- 任务记录在 Agent 内存中保留 `task_retention_sec`（默认 3600 秒）

### 4.5 取消

```http
POST /api/v1/tasks/{id}/cancel
→ { "cancelling": true }
```

已结束或不存在返回 `{"cancelling": false}`；不存在时先返回 404。

### 4.6 curl 示例

完整的可复制命令见 [scripts/curl-examples.sh](../scripts/curl-examples.sh)。
自签 HTTPS 场景请使用 `--cacert`，不要默认 `-k`。
