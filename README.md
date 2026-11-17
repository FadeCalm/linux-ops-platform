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

**仍不是完整的认证与授权。** 平台现在有用户名/密码登录（bcrypt）、管理员与子用户
两种角色、按归属隔离的服务器视图，以及短期 JWT 和长期 API 令牌（脚本用）；
但依然没有 Agent 认证、mTLS、命令审批、细粒度 RBAC 或多因素认证。账号口令、
会话 Cookie、JWT 和 API 令牌都走明文 HTTP，内网可被抓包；拿到管理员凭证就能在
受管主机上执行任意命令。

- 首次启动自动创建超级管理员：用户名 `admin`，初始密码取
  `auth.access_password`（未配置时为 `changeme`）
- 管理员可修改自己的密码、创建子用户、重置子用户密码；子用户只能看到并操作
  自己名下的服务器，管理员可见全部服务器
- 默认只监听 `127.0.0.1`
- 跨机使用必须绑定**内网地址**，并用防火墙/安全组限制到管理网段
- **严禁暴露到公网**
- 本阶段**不是**生产安全配置；正式生产前必须先完成 M8 安全加固

审计记录会带上操作者用户名（未登录时为 `anonymous`），平台不提供伪造身份、
绕过审计或隐藏痕迹的能力。

---

## 功能

| 模块 | 能力 |
|---|---|
| 登录 | 用户名/密码（bcrypt）、12 小时服务端会话、登录失败限流、自助改密、退出 |
| 令牌 | 脚本认证：`POST /auth/token` 换短期 JWT；页面创建/吊销长期 API 令牌（存哈希、可设过期、记录最后使用） |
| 用户管理 | 管理员创建子用户、重置密码、删除；服务器按归属账号隔离，管理员可见全部 |
| 概览 | 服务器总数/在线/离线、Agent 版本分布、近期任务、成功率（子用户仅统计自己名下） |
| 服务器管理 | 增删改查、归属账号标识、名称留空自动取主机名、分组、标签、搜索、批量健康检查、Agent 信息 |
| 命令执行 | 多机选择、命令/工作目录/超时、实时 stdout/stderr、逐机标签页、取消/重试/下载 |
| 单机执行 | 点击主机进入独立页面：快捷命令、实时输出、取消/重试、最近任务 |
| 文件管理 | 目录浏览/切换、新建、编辑保存（并发冲突检测）、分片上传、下载（Range 断点）、重命名、递归删除 |
| 交互终端 | xterm.js + PTY，支持 `cd`/`vim`/`top`/`Ctrl+C`/窗口缩放，空闲与最长时长限制 |
| Agent 升级 | 页面单台/批量检查与升级：扫描 MinIO 桶按文件名发现新版本、SHA256 校验、原地重启、保留 `agent.old` |
| 任务历史 | 状态/服务器/时间筛选、分页、详情、日志回放、取消/重试/下载 |
| 可靠性 | 幂等键、进程组终止、超时、后端重启恢复、WebSocket 断线重放 |
| 审计 | 任务与资源操作记录（含操作用户名；子用户默认只看到自己的记录） |

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
curl -sS http://127.0.0.1:8089/api/v1/health
sudo bash tests/smoke.sh
```

浏览器打开 `http://<服务器IP>:8089/`（本机为 `http://127.0.0.1:8089/`）。
详细步骤、配置、备份、故障排查见 [docs/deployment.md](docs/deployment.md)。

## 本地开发

连接信息（MySQL + MinIO）统一放在 `configs/local.yaml`，该文件含明文口令、
已被 `.gitignore` 排除。首次使用从模板复制后填写：

```bash
cp configs/backend.yaml configs/local.yaml   # 填写 mysql / minio 两段
```

`auth.access_password` 只用于初始化超级管理员 `admin` 的密码：数据库中不存在
`admin` 时用该值（或环境变量 `ACCESS_PASSWORD`）创建，留空则使用 `changeme`。
登录页始终要求用户名和密码；首次登录后请在右上角「密码」中修改。之后该配置项
不再覆盖数据库中的密码。

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

# 4) 前端开发服务器（另一个终端，代理 /api 到 8089）
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
- [docs/deployment.md](docs/deployment.md) — 单机演示安装、配置、安全清单、备份恢复、排障
- [docs/production-deploy.md](docs/production-deploy.md) — 生产部署：nginx + 现有 MySQL/MinIO、Agent 白名单安装、Agent 混淆构建
- [docs/progress.md](docs/progress.md) — 已验证 / 待验证清单
- [scripts/curl-examples.sh](scripts/curl-examples.sh) — 可直接复制的 curl 示例

## 已知限制

- 账号体系只有 admin / user 两种角色和“按账号归属”的服务器隔离；没有细粒度 RBAC、令牌 scope 或多因素认证
- 无 TLS；账号口令、会话 Cookie、JWT 与 API 令牌均为明文 HTTP，仅限隔离网络
- Agent 没有请求认证，只靠 `allowed_ips` 来源白名单与防火墙；白名单主机仍可绕过平台直接执行命令
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
| M8 | 认证、RBAC、mTLS、审批、审计防篡改 | 未开始（当前有用户名/密码、JWT/API 令牌与 admin/user 两级角色） |
