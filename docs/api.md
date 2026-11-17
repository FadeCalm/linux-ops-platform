# API 参考

本平台有两套 HTTP API：

- **管理后端**：浏览器与 curl 使用，前缀 `/api/v1`
- **Agent**：仅由后端调用（也可用于排障），前缀 `/api/v1`，默认端口 `19090`

> 管理后端要求先登录（见 §2.0），其余接口均需携带会话 Cookie；Agent 的
> 19090 端口没有请求认证，只接受 `allowed_ips` 中列出的来源 IP/CIDR，其他来源
> 一律返回 403。请勿在公网暴露任何端口。所有示例都假设从白名单主机访问。

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

### 2.0 登录与账号

除 `GET /api/v1/health` 与下列登录/换令牌接口外，所有 `/api/v1` 路径（含
WebSocket 握手）都要求认证。三种凭证任选其一：

- 浏览器：`lop_session` Cookie（HttpOnly + SameSite=Lax）
- 脚本：`Authorization: Bearer <JWT>`（短期，`POST /auth/token` 换取）
- 脚本/第三方：`Authorization: Bearer lop_...`（API 令牌，页面创建与吊销）

未认证返回 `401 {"code":401,"message":"未认证：请先登录或提供有效令牌"}`。
权限不足返回 `403 {"code":403,"message":"需要管理员权限"}` 或资源归属不匹配的 403。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/auth/status` | 返回 `{required, authenticated, user?}`；Cookie 或 Bearer 均可 |
| POST | `/api/v1/auth/login` | 请求体 `{username, password}`；成功时下发 `lop_session` Cookie |
| POST | `/api/v1/auth/token` | 请求体 `{username, password}`；返回短期 JWT（不设置 Cookie） |
| POST | `/api/v1/auth/logout` | 撤销当前 Cookie 会话并清除 Cookie（不影响 JWT/API 令牌） |
| POST | `/api/v1/auth/password` | 已登录用户改密，请求体 `{old_password, new_password}` |

```bash
curl -c cookie.txt -X POST http://127.0.0.1:8089/api/v1/auth/login \
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"changeme"}'
curl -b cookie.txt http://127.0.0.1:8089/api/v1/servers

# 短期 JWT（默认 120 分钟，由 auth.jwt_minutes 控制）
JWT=$(curl -s -X POST http://127.0.0.1:8089/api/v1/auth/token \
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"changeme"}' \
  | sed -E 's/.*"access_token":"([^"]+)".*/\1/')
curl -H "Authorization: Bearer $JWT" http://127.0.0.1:8089/api/v1/servers
```

- 首次启动自动创建 `admin`：密码取 `auth.access_password`，留空则为 `changeme`；
  之后密码只存数据库，配置项不再覆盖。请在首次登录后立即修改。
- Cookie 是随机 Token，数据库只保存其 SHA-256；会话有效期为 `session_hours`
  （默认 12 小时）。改密会撤销该用户的全部旧会话，再为当前浏览器下发新 Cookie。
- JWT 为 HS256 签名，claims 含 `sub/usr/role/pv/iss/iat/exp`，有效期默认 120 分钟
  （`auth.jwt_minutes`）。`pv` 是密码哈希的指纹：改密后旧 JWT 立即失效。签名密钥
  `auth.jwt_secret` 留空时首次启动自动生成并存入 `app_settings`；更换密钥会让所有
  未过期 JWT 失效。JWT 无刷新机制，过期后重新调用 `/auth/token`。
- API 令牌形如 `lop_<43 字符>`，数据库只保存 SHA-256 与用于展示的前缀；明文只在
  创建响应中出现一次。令牌权限等同于创建者账号（管理员令牌可管理全部；子用户
  令牌只能访问自己名下的服务器）。管理员重置密码或删除账号会吊销该用户全部令牌；
  用户自助改密不会吊销 API 令牌（脚本凭证与登录口令分开管理）。
- 登录/换 JWT 失败按 IP 限流：每分钟 10 次，超出返回 `429`；失败不会透露用户名
  是否存在，并固定延迟 300ms。
- 角色只有 `admin` 和 `user` 两种：管理员可见全部服务器/任务/审计；子用户只能
  访问归属自己（`owner_id`）的资源，跨账号访问返回 403。
- 明文 HTTP 下 Cookie、JWT 与 API 令牌都可被内网抓包，这不是生产级认证方案。

### 2.0.1 API 令牌管理

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/auth/tokens` | 当前账号的令牌；管理员可加 `?user_id=N` 查看指定用户 |
| POST | `/api/v1/auth/tokens` | 创建令牌，请求体 `{name, expires_in_days}`；`0` 表示永不过期 |
| DELETE | `/api/v1/auth/tokens/{id}` | 吊销令牌；本人或管理员 |

```bash
# 用 Cookie 会话创建（明文 token 只在这次响应里出现）
curl -b cookie.txt -X POST http://127.0.0.1:8089/api/v1/auth/tokens \
  -H 'Content-Type: application/json' -d '{"name":"backup-script","expires_in_days":90}'

# 之后脚本直接携带令牌
curl -H "Authorization: Bearer lop_xxx" http://127.0.0.1:8089/api/v1/servers
```

- 每个账号最多 20 个令牌；响应中的 `item` 不含明文，`prefix` 仅用于列表展示。
- `last_used_at` 最多每 5 分钟更新一次，避免高频脚本放大数据库写入。
- 创建/吊销会写入审计事件 `token.create` / `token.revoke`。

### 2.0.2 用户管理（仅管理员）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/users` | 用户列表，含 `role`、`server_count`、`created_by` |
| POST | `/api/v1/users` | 新建子用户，请求体 `{username, password}`；用户名 3–32 位 `[A-Za-z0-9_.-]` |
| PUT | `/api/v1/users/{id}/password` | 重置指定用户密码（不能重置自己），并撤销其全部会话与 API 令牌 |
| DELETE | `/api/v1/users/{id}` | 删除子用户（同时删除其会话与令牌）；名下仍有服务器时返回 409，不能删除自己或管理员 |

```bash
curl -b cookie.txt -X POST http://127.0.0.1:8089/api/v1/users \
  -H 'Content-Type: application/json' -d '{"username":"alice","password":"至少6位"}'
```

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
| POST | `/api/v1/agents/check` | 批量健康检查，body `{server_ids:[...]}` |
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

- 响应带 `owner_id` / `owner_name`。新建服务器的归属固定为当前登录账号；
  子用户只能列出、读取和修改自己名下的服务器（直接访问他人 ID 返回 403），
  管理员可访问全部
- 批量接口（健康检查、Agent 更新检查/升级）中只要包含一台无权访问的服务器，
  整个请求返回 403
- `ip` 必填；`agent_port` 默认 `19090`
- `name` 新增时可省略：后端会探测一次 Agent，成功则用其上报的 `hostname`
  作为名称，失败则暂用 `ip` 作为名称（服务器仍会创建，`status=offline`）
- 更新时 `name`、`ip` 必填
- `tags` 中的名字不存在时会自动创建
- 新增服务器未探测时初始 `status` 为 `unknown`

批量健康检查响应（`server_ids` 最多 200 个，并发 4）：

```json
{ "code": 0, "data": { "items": [
  { "server_id": 2, "name": "web-01", "ip": "10.0.0.11", "agent_port": 19090,
    "online": true, "hostname": "web-01", "os": "linux", "arch": "amd64",
    "version": "0.1.7" },
  { "server_id": 3, "name": "db-01", "ip": "10.0.0.12", "agent_port": 19090,
    "online": false, "error": "dial tcp 10.0.0.12:19090: connect: connection refused" }
] } }
```

检查结果会同步写回服务器状态、主机名、系统、架构与 Agent 版本。

### 2.3 分组与标签

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/groups` | 列出全部分组（不分页，所有登录用户可读） |
| POST | `/api/v1/groups` | 新建 `{name, description}`（仅管理员） |
| PUT | `/api/v1/groups/{id}` | 更新 `{name, description}`（仅管理员） |
| DELETE | `/api/v1/groups/{id}` | 删除；相关服务器的 `group_id` 置空（仅管理员） |
| GET | `/api/v1/tags` | 列出全部标签（不分页，所有登录用户可读） |
| POST | `/api/v1/tags` | 新建 `{name}`（仅管理员） |
| DELETE | `/api/v1/tags/{id}` | 删除并清理关联（仅管理员） |

分组与标签是全局配置；子用户可以使用，但不能增删改。

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
| `server_ids` | 是 | 至少一个；重复 ID 自动去重；必须全部属于当前账号（管理员除外），否则 403 |
| `idempotency_key` | 否 | 唯一键；同一账号内重复时直接返回原任务，其他账号使用同一键返回 409 |

任务的 `owner_id` / `owner_name` / `created_by` 记录创建者；子用户只能列出和操作
自己的任务（含子任务、日志、取消、重试、WebSocket），管理员可访问全部。

响应 200：

```json
{
  "code": 0,
  "data": {
    "task": {
      "id": "6f0a...", "command": "uname -a && df -h", "work_dir": "/tmp",
      "timeout_ms": 30000, "status": "PENDING", "total": 2,
      "succeeded": 0, "failed": 0, "cancelled": 0,
      "created_by": "admin", "owner_id": 1, "owner_name": "admin",
      "created_at": "2026-11-06T10:00:00Z",
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

子用户只能看到自己创建的任务；审计接口 `GET /api/v1/audit` 同理，子用户默认
只能看到 `operator` 为自己的记录。

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

返回 `{items,total,page,page_size}`。`operator` 为操作用户名（未登录动作记为
`anonymous`）；子用户默认只能看到自己的记录。

主要 action：

`auth.login`、`auth.login.failed`、`auth.logout`、`auth.password.change`、
`user.create`、`user.password.reset`、`user.delete`、
`task.create`、`task.cancel`、`target.cancel`、`target.retry`、
`server.create/update/delete`、`group.create/update/delete`、
`tag.create/delete`、`terminal.open/close`。

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
`wxgzt-ops-agent-<version>-linux-<arch>.tar.gz` 取高于自身的最大版本；若
`manifest.json` 存在则一并参考，取两者中较高者，并优先采用 manifest 中的
`notes`/`sha256`。校验和缺失时再查 `SHA256SUMS`；仍缺失则默认跳过校验
（`update_require_checksum: true` 可强制要求）。随后下载 tarball → 校验
SHA256 → 替换自身二进制（旧文件保留为 `agent.old`）→ `exec` 原地重启。

因此发布新版本只需把命名正确的 tar.gz 放进桶里，不必手改 manifest；
桶须允许匿名 `s3:ListBucket` + `s3:GetObject`。注意当前发布流程把校验和写进
`agent-install.sh`，桶里没有 `SHA256SUMS` 时自更新只能校验下载完整性；
要强制校验需额外保留 `SHA256SUMS` 并开启 `update_require_checksum`。
`/update/check` 额外返回
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

### 3.5 终端 WebSocket（PTY）

```
ws://<backend>/api/v1/ws/terminal/{serverId}
```

- 后端校验服务器存在与并发上限（`websocket.max_terminal_sessions`，超出返回
  HTTP 503），升级后代理到 Agent 的 `ws://<agent>/api/v1/ws/terminal`；
  浏览器不直连 Agent。
- **二进制帧**：双向原始终端字节（按键与输出）。
- **文本帧**：控制消息。
  - 客户端 → 服务端：`{"type":"resize","cols":120,"rows":30}`
  - 服务端 → 客户端（连接失败时）：`{"type":"error","message":"..."}`
- 会话限制由 Agent 执行：空闲 `terminal.idle_timeout_sec`（默认 600s）、
  最长 `terminal.max_duration_sec`（默认 3600s）、并发 `terminal.max_sessions`
  （默认 4）。`history_mode: memory` 时不会写入受管用户的 `~/.bash_history`。
- 审计记录 `terminal.open` / `terminal.close`。

---

## 4. Agent API

Agent 只被后端调用，也可在**白名单主机**上用 curl 排障。Agent 返回裸 JSON
（无 `code/data` 信封）。`allowed_ips` 之外的来源在进入路由前即返回：

```json
{ "error": "forbidden: source ip is not in allowed_ips" }
```

其余错误格式：

```json
{ "error": "task not found" }
```

### 4.1 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/health` | `{"status":"ok","uptime_sec":123}` |
| GET | `/api/v1/info` | hostname/os/arch/version/running_tasks/max_concurrent |
| GET | `/api/v1/version` | `{"version":"0.2.0"}` |
| POST | `/api/v1/tasks` | 提交任务（按 `task_id` 幂等） |
| GET | `/api/v1/tasks/{id}` | 查询状态 |
| GET | `/api/v1/tasks/{id}/logs` | 游标增量日志 |
| POST | `/api/v1/tasks/{id}/cancel` | 取消并杀进程组 |
| GET | `/api/v1/fs/list` `stat` `read` `download` | 浏览 / 读取 / 下载 |
| PUT | `/api/v1/fs/write` `upload` | 原子保存 / 分片上传 |
| POST | `/api/v1/fs/op` | mkdir / touch / rename / chmod / delete |
| GET | `/api/v1/ws/terminal` | WebSocket：PTY 会话（只由后端代理，不直连浏览器） |
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
