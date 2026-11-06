# Linux 集中运维平台

一个可运行的 Linux 集中运维平台：管理服务器、向一台或多台主机下发命令，
实时查看 stdout/stderr、退出码与耗时，支持取消、超时、断线重放、后端重启恢复，
并持久化任务历史与日志；附带文件管理、交互式终端（PTY）和 Agent 在线升级。

技术栈：

- 前端：Vue 2.7 + Element UI 2 + Vue Router 3 + Vuex 3 + Axios（Vite 构建，黑/白主题可手动切换）
- 管理后端：Go 1.22 + Gin + GORM
- Agent：Go 1.22 纯标准库 HTTP，`os/exec` + `/bin/bash -c`，进程组隔离
- 存储：MySQL 8；任务日志落本地文件；MinIO 存放 Agent 发布包（自更新源）

---

## ⚠️ 安全警告（务必阅读）

**只有统一访问口令，不是真正的认证与授权。** 没有多用户账号、RBAC、JWT、
API Token、Agent 认证、mTLS 或命令审批。知道口令的任何人都可以在受管主机上
执行任意命令；口令和会话 Cookie 走明文 HTTP，内网可被抓包。

- 口令配置项：`auth.access_password`（或环境变量 `ACCESS_PASSWORD`），留空即关闭
- 默认只监听 `127.0.0.1`
- 跨机使用必须绑定**内网地址**，并用防火墙/安全组限制到管理网段
- **严禁暴露到公网**
- 本阶段**不是**生产安全配置；正式生产前必须先完成 M8 安全加固

审计记录的 operator 恒为 `anonymous`，平台不提供伪造身份、绕过审计或隐藏痕迹的能力。

---

## 功能

| 模块 | 能力 |
|---|---|
| 登录 | 统一访问口令、12 小时会话、登录失败限流、退出 |
| 概览 | 服务器总数/在线/离线、Agent 版本分布、近期任务、成功率 |
| 服务器管理 | 增删改查、名称留空自动取主机名、分组、标签、搜索、批量健康检查、Agent 信息 |
| 命令执行 | 多机选择、命令/工作目录/超时、实时 stdout/stderr、逐机标签页、取消/重试/下载 |
| 单机执行 | 点击主机进入独立页面：快捷命令、实时输出、取消/重试、最近任务 |
| 文件管理 | 目录浏览/切换、新建、编辑保存（并发冲突检测）、分片上传、下载（Range 断点）、重命名、递归删除 |
| 交互终端 | xterm.js + PTY，支持 `cd`/`vim`/`top`/`Ctrl+C`/窗口缩放，空闲与最长时长限制 |
| Agent 升级 | 页面单台/批量检查与升级：扫描 MinIO 桶按文件名发现新版本、SHA256 校验、原地重启、保留 `agent.old` |
| 任务历史 | 状态/服务器/时间筛选、分页、详情、日志回放、取消/重试/下载 |
| 可靠性 | 幂等键、进程组终止、超时、后端重启恢复、WebSocket 断线重放 |
| 审计 | 任务与资源操作记录（anonymous） |

## 架构

```text
浏览器 (Vue 2.7)
   │ REST + WebSocket
   ▼
管理后端 (Go/Gin) ── MySQL 8
   │ HTTP（后端主动访问，拉模式）
   ▼
Agent A/B/N ── /bin/bash -c ── 进程组
```

- 后端主动连接 Agent；Agent 不回连后端
- Agent 用游标保存 stdout/stderr 缓冲；后端按偏移增量拉取、落盘、广播
- WebSocket 每帧带单调 `seq`，重连可携带游标回放
- 后端重启后继续跟踪未完成任务；无法确认结果时标 `UNKNOWN`，**绝不重复执行**

详见 [docs/architecture.md](docs/architecture.md)。

## 目录结构

```text
backend/      Go 管理后端（独立 go.mod）
agent/        Go Agent（独立 go.mod，纯 Go，可交叉编译）
frontend/     Vue 2.7 + Element UI（Vite）
database/     schema.sql + seed.sql
configs/      backend.yaml / agent.yaml 模板（不含密钥）；local.yaml 本机配置（gitignore）
deploy/       docker-compose.yml（MySQL/MinIO）、systemd units、nginx 示例、.env.example
scripts/      build.sh / install.sh / uninstall.sh / agent-install.sh / curl-examples.sh
tests/        smoke.sh（真实端到端冒烟）
docs/         架构、API、部署、验证进度、目录与功能总览
bin/          构建产物与发布包（gitignore）：二进制、agent tar.gz、minio_upload.py
```

每个目录与文件的职责、配置清单、功能清单见
[docs/overview.md](docs/overview.md)。

## 快速开始（Linux 服务器）

前置：Go 1.22+、Node 18+、Docker + Compose 插件；Linux **amd64**。

```bash
cd linux-ops-platform

# 1) 配置密码
cd deploy
cp .env.example .env
# 编辑 .env：MySQL 密码、MinIO 密码、ACCESS_PASSWORD（平台登录口令）
# 建议用 openssl rand -hex 16 生成
cd ..

# 2) 一键安装（仅本机访问）
sudo bash scripts/install.sh

# 或允许管理网段访问（务必同时限制防火墙）
sudo bash scripts/install.sh --lan-ip 10.0.0.10

# 3) 验证
curl -sS http://127.0.0.1:8080/api/v1/health
sudo bash tests/smoke.sh
```

浏览器打开 `http://<服务器IP>:8080/`（本机为 `http://127.0.0.1:8080/`）。
详细步骤、配置、备份、故障排查见 [docs/deployment.md](docs/deployment.md)。

## 本地开发

连接信息（MySQL + MinIO）统一放在 `configs/local.yaml`，该文件含明文口令、
已被 `.gitignore` 排除。首次使用从模板复制后填写：

```bash
cp configs/backend.yaml configs/local.yaml   # 填写 mysql / minio 两段
```

需要登录校验时，在 `configs/local.yaml` 的 `auth.access_password` 填一个口令
（留空则跳过登录页）；启动时也可以用 `ACCESS_PASSWORD=... ./bin/backend ...`
覆盖。

```bash
# 1) 基础设施（二选一）
#    a. 本机 docker：cd deploy && cp .env.example .env && docker compose up -d
#    b. 已有 MySQL/MinIO：直接在 configs/local.yaml 填地址和账号

# 2) 后端（另一个终端）
./bin/backend.exe -config configs/local.yaml
#    或：cd backend && go run . -config ../configs/local.yaml

# 3) Agent（另一个终端，仅 Linux 可真实执行）
cd agent
go run . -config ../configs/agent.yaml

# 4) 前端开发服务器（另一个终端，代理 /api 到 8080）
cd frontend
npm install
npm run dev        # http://127.0.0.1:5173
```

发布 Agent 包到 MinIO 时同样读取 `configs/local.yaml` 的 `minio` 段：

```bash
python bin/minio_upload.py
```

环境变量（`DB_PASSWORD`、`DB_HOST`、`MINIO_ACCESS_KEY` 等）仍然可用，
优先级高于配置文件。

> Windows 开发机上 Agent 只能编译和响应健康检查；真正执行命令依赖
> `/bin/bash` 与 Unix 进程组，必须在 Linux 上验证。

## 构建与测试

```bash
# 后端
cd backend && gofmt -l . && go build ./... && go vet ./... && go test ./...

# Agent
cd agent && gofmt -l . && go build ./... && go vet ./... && go test ./...

# 交叉编译（开发机）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/agent-amd64 .

# 前端
cd frontend && npm ci && npm run build

# 前端（自定义前后端前缀，配合 deploy/nginx-prefix.conf.example）
# VITE_BASE_PATH: 前端挂载路径，产物落在 dist/<前缀>/；VITE_API_BASE: 后端请求基址
# Windows Git Bash 请去掉前导斜杠（MSYS 会改写 /ops-wxgzt/ 这类变量值），
# 或写入 frontend/.env.local（模板见 frontend/.env.example）
VITE_BASE_PATH=/ops-wxgzt/ VITE_API_BASE=/ops-server/api/v1 npm run build
# -> dist/ops-wxgzt/index.html + dist/ops-wxgzt/assets/...
```

当前验证状态（哪些已在本机验证、哪些必须在 Linux 服务器完成）见
[docs/progress.md](docs/progress.md)。

## 文档

- [docs/overview.md](docs/overview.md) — 目录结构、配置文件清单、功能清单
- [docs/architecture.md](docs/architecture.md) — 架构、数据模型、状态机、恢复策略、演进设计
- [docs/api.md](docs/api.md) — 后端 API、Agent API、WebSocket 协议
- [docs/deployment.md](docs/deployment.md) — 安装、配置、安全清单、备份恢复、排障
- [docs/progress.md](docs/progress.md) — 已验证 / 待验证清单
- [scripts/curl-examples.sh](scripts/curl-examples.sh) — 可直接复制的 curl 示例

## 已知限制

- 仅有统一访问口令（`auth.access_password`），没有多用户账号与权限模型；仅限隔离网络
- 无 TLS；链路为明文 HTTP
- 输出为轮询拉取（默认 300ms），不是逐字节实时；M4 的 PTY 才提供真正实时交互
- 日志按字节分块传输/读取，跨块边界的多字节 UTF-8 字符可能显示为替换符（`�`）
- `/tmp` 在 systemd 沙箱中是服务私有目录；`/home`、`/root` 不可访问
- 日志需运维自行清理；MinIO 大日志在 M6 接入
- 单 MySQL、单后端，无高可用

## 路线图

| 阶段 | 内容 | 状态 |
|---|---|---|
| M1–M3 | 基础、单机执行、批量与可靠性 | 已完成 |
| M4 | 交互式终端（xterm.js + PTY） | 已完成 |
| M5 | 命令模板库与版本 | 未开始 |
| M6 | MinIO 大日志、Agent 包管理与升级 | 升级已完成；大日志未开始 |
| M7 | 高可用与容量 | 未开始 |
| M8 | 认证、RBAC、mTLS、审批、审计防篡改 | 未开始（当前仅有统一访问口令） |
