# 项目总览：目录、配置与功能清单

> 面向维护者的索引文档。回答三个问题：**代码在哪**、**配置在哪**、**功能有哪些**。
> 安装步骤见 [deployment.md](deployment.md)，接口细节见 [api.md](api.md)，
> 设计取舍见 [architecture.md](architecture.md)，验证记录见 [progress.md](progress.md)。

## 1. 当前范围

三端 + 数据库的可运行版本，Agent 版本 **0.1.7**：

- 服务器/分组/标签管理，Agent 在线状态与版本
- 单机与批量命令执行（`bash -c`），实时 stdout/stderr、取消、超时、重试
- 文件管理（浏览、编辑、上传、下载、重命名、删除）
- 交互式终端（WebSocket + PTY + xterm.js）
- Agent 在线升级（MinIO 制品桶按文件名发现新版本，有校验和时做 SHA256 校验，原地重启）
- 用户名/密码登录（bcrypt、服务端会话）、admin/user 角色、管理员管理子用户
- 脚本认证：短期 JWT（`/auth/token`）与长期 API 令牌（页面创建/吊销、存哈希、可设过期）
- 服务器与任务按账号归属隔离：管理员可见全部，子用户只见自己名下
- 任务历史、日志回放与下载、带用户名的审计

尚未实现：细粒度 RBAC、令牌 scope、mTLS、命令模板库（M5）、MinIO 大日志（M6）、高可用（M7）。

## 2. 目录结构

```text
linux-ops-platform/
├── backend/                     Go 管理后端（独立 go.mod）
│   ├── main.go                  启动、静态前端托管、优雅退出
│   └── internal/
│       ├── api/                 HTTP 路由与 handler
│       │   ├── router.go        路由注册、中间件（CORS/请求体限制/鉴权/审计）
│       │   ├── auth.go          账号登录、会话 Cookie、JWT 签发、登录限流
│       │   ├── jwt.go           内置 HS256 JWT 签发/校验
│       │   ├── tokens.go        API 令牌签发/列表/吊销
│       │   ├── users.go         子用户管理（仅管理员）
│       │   ├── tasks.go         任务创建/查询/取消/重试
│       │   ├── servers.go       服务器 CRUD、批量健康检查、心跳
│       │   ├── groups.go        分组
│       │   ├── tags.go          标签
│       │   ├── fs.go            文件管理（转发到 Agent）
│       │   ├── terminal.go      终端 WebSocket 代理
│       │   ├── ws.go            任务日志 WebSocket
│       │   └── agentupdate.go   Agent 检查/升级（单台与批量）
│       ├── agentclient/         调用 Agent 的 HTTP 客户端
│       ├── config/              YAML 配置 + 环境变量覆盖
│       ├── database/            MySQL 连接与自动迁移
│       ├── logstore/            stdout/stderr 落盘与分片索引
│       ├── models/              GORM 模型
│       ├── scheduler/           心跳、派发、轮询、状态机
│       └── ws/                  WebSocket Hub（seq、重放缓冲）
│
├── agent/                       Go Agent（独立 go.mod，纯标准库，CGO_ENABLED=0）
│   ├── main.go                  HTTP 启动、版本号（-ldflags 注入）
│   └── internal/
│       ├── api/                 Agent HTTP 接口（health/info/tasks/fs/terminal/update）
│       ├── config/              YAML 配置 + 环境变量覆盖
│       ├── exec/                命令执行引擎、输出缓冲、进程组控制
│       ├── term/                PTY 会话管理（history_mode 等）
│       └── update/              自更新：ListObjectsV2 发现、下载、校验、替换、exec
│
├── frontend/                    Vue 2.7 + Element UI 2 + Vite
│   ├── src/
│   │   ├── App.vue              外壳：顶栏、侧栏、主题、退出登录
│   │   ├── main.js              入口
│   │   ├── api/                 axios 封装、接口清单、路径基址
│   │   ├── router/              路由与登录守卫
│   │   ├── store/               Vuex（limits、后端在线状态、authRequired）
│   │   ├── styles/theme.scss    黑/白主题变量与 Element UI 覆盖
│   │   ├── utils/               主题与格式化工具
│   │   └── views/
│   │       ├── Login.vue        访问口令登录页
│   │       ├── Dashboard.vue    概览
│   │       ├── Servers.vue      服务器管理、批量检查/升级
│   │       ├── Execute.vue      批量命令执行
│   │       ├── ServerExec.vue   单机执行
│   │       ├── Files.vue        文件管理
│   │       ├── History.vue      任务历史
│   │       ├── TerminalPick.vue 选择主机进入终端
│   │       └── Terminal.vue     xterm.js 交互终端
│   ├── vite.config.js           构建产物目录、dev 代理
│   └── .env.example             VITE_BASE_PATH / VITE_API_BASE
│
├── configs/
│   ├── backend.yaml             后端配置模板（入库，无密钥）
│   ├── agent.yaml               Agent 配置模板（入库）
│   └── local.yaml               本机开发配置（gitignore，含明文口令）
│
├── database/
│   ├── schema.sql               手工建表脚本（auto_migrate=false 时使用）
│   ├── seed/seed.sql            示例数据
│   └── migrations/              预留目录（当前为空）
│
├── deploy/
│   ├── docker-compose.yml       MySQL 8 + MinIO
│   ├── .env.example             基础设施口令模板
│   ├── nginx.conf.example       同域反向代理
│   ├── nginx-prefix.conf.example 前后端不同前缀的反向代理
│   └── systemd/                 后端与 Agent 的 unit（含沙箱选项）
│
├── scripts/
│   ├── build.sh                 构建三端（含 Linux 交叉编译）
│   ├── install.sh               一键安装后端 + 本机 Agent + 前端
│   ├── uninstall.sh             卸载
│   ├── agent-install.sh         在其它 Linux 主机安装 Agent（从制品桶下载）
│   └── curl-examples.sh         API 示例
│
├── tests/smoke.sh               端到端冒烟测试
├── docs/                        架构、API、部署、进度、本文件
├── bin/                         构建产物与发布包（gitignore）
│   ├── backend(.exe)            后端二进制
│   ├── wxgzt-ops / wxgzt-ops-linux-amd64  Agent 二进制
│   ├── wxgzt-ops-agent-<ver>-linux-amd64.tar.gz  Agent 发布包
│   ├── release/                 Agent 制品（上传 MinIO）+ server/ 管理端部署包
│   ├── minio_upload.py          发布工具：上传包 + 渲染安装脚本（内置地址/校验和）+ 桶策略
│   ├── smoke_e2e.py             端到端脚本
│   └── pkg/wxgzt-ops/     发布包内容（agent、agent.yaml、service、agent-install.sh、README）
└── linux-ops-platform-ai-spec.md  原始需求规格
```

`bin/`、`frontend/dist/`、`data/`、`configs/local.yaml`、`deploy/.env`、
`frontend/.env.local` 都被 `.gitignore` 排除。

## 3. 配置文件清单

| 文件 | 读取方 | 是否入库 | 用途 / 注意事项 |
|---|---|---|---|
| `configs/backend.yaml` | 后端 | 是（模板） | 监听地址、MySQL、MinIO、调度、WebSocket、访问口令；口令留空 |
| `configs/local.yaml` | 本机后端 + `bin/minio_upload.py` | **否** | 含明文 MySQL/MinIO 口令；开发机联调用 |
| `configs/agent.yaml` | Agent | 是 | 监听、`allowed_ips`、并发、终端、自更新地址 |
| `bin/pkg/wxgzt-ops/agent.yaml` | 发布包内 Agent | 否（bin 整体忽略） | 与 `configs/agent.yaml` 内容一致，打包时复制；安装后即 `/opt/wxgzt-ops/agent.yaml` |
| `deploy/.env` | `docker compose` + `scripts/install.sh` | **否** | MySQL/MinIO 口令、`ACCESS_PASSWORD` |
| `deploy/.env.example` | 人工复制 | 是（模板） | 同上，占位值 |
| `deploy/systemd/*.service` | systemd | 是 | `EnvironmentFile` 指向 `/etc/wxgzt-ops/*.env` |
| `frontend/.env.example` | Vite 构建 | 是（模板） | `VITE_BASE_PATH`、`VITE_API_BASE`，不含密钥 |
| `frontend/.env.local` | Vite 构建 | **否** | 本机构建前缀，覆盖 example |
| `deploy/nginx*.conf.example` | nginx | 是（模板） | 同域/自定义前缀反向代理，需透传 Upgrade |

### 3.1 后端环境变量（优先级高于 YAML）

| 变量 | 对应配置 |
|---|---|
| `LISTEN_ADDR` | `listen_addr` |
| `DB_HOST` / `DB_PORT` / `DB_USER` / `DB_PASSWORD` / `DB_NAME` / `DB_DSN` | `mysql.*` |
| `LOG_DIR` | `log_dir` |
| `ALLOW_ORIGIN` | `allow_origin` |
| `FRONTEND_DIR` | `frontend_dir` |
| `ACCESS_PASSWORD` | `auth.access_password` |
| `SESSION_HOURS` | `auth.session_hours` |

### 3.2 Agent 环境变量

`AGENT_LISTEN_ADDR`、`AGENT_LISTEN_PORT`、`AGENT_LOG_DIR`。

完整的配置项含义见 [deployment.md §5](deployment.md#5-配置参考)。

## 4. 功能清单

| 模块 | 能力 | 前端入口 | 主要实现 | 状态 |
|---|---|---|---|---|
| 登录 | 用户名/密码（bcrypt）、12h 服务端会话、登录限流、自助改密、退出 | `/login` | `backend/internal/api/auth.go`、`frontend/src/views/Login.vue` | 已实现，本机验证 |
| 令牌 | 短期 JWT（`POST /auth/token`）；API 令牌创建/吊销、前缀与最后使用展示、过期时间 | 顶栏「令牌」 | `auth.go`、`jwt.go`、`tokens.go`、`App.vue` | 已实现，本机验证 |
| 用户管理 | 管理员创建子用户、重置密码、删除；查看/吊销子用户令牌；显示名下服务器数 | `/users` | `users.go`、`permissions.go`、`Users.vue` | 已实现，本机验证 |
| 概览 | 在线/离线数、Agent 版本分布、近期任务、成功率（按归属过滤） | `/dashboard` | `backend/internal/api/router.go` `Overview` | 已实现 |
| 服务器管理 | 增删改查、归属账号标识、名称可留空自动取主机名、分组、标签、搜索、批量健康检查、Agent 信息 | `/servers` | `servers.go`、`groups.go`、`tags.go`、`permissions.go` | 已实现，真实主机验证 |
| 批量执行 | 多机选择、命令/目录/超时、实时输出、取消、重试、下载日志 | `/execute` | `tasks.go`、`scheduler/`、`ws.go`、`logstore/` | 已实现，Linux 验证 |
| 单机执行 | 快捷命令、实时输出、最近任务、取消/重试 | `/servers/:id/execute` | `ServerExec.vue`、`tasks.go` | 已实现 |
| 文件管理 | 目录浏览、编辑保存（冲突检测）、分片上传、Range 下载、重命名、递归删除 | `/files` | `backend/internal/api/fs.go`、`agent/internal/api/fs.go` | 已实现 |
| 交互终端 | xterm.js + PTY、cd/vim/top、Ctrl+C、窗口缩放、空闲/最长时长限制 | `/terminal` | `terminal.go`（两端）、`agent/internal/term/` | 已实现，Linux 验证 |
| Agent 升级 | 桶扫描发现版本、有校验和时做 SHA256 校验、备份 `agent.old`、exec 原地重启、单台/批量 | `/servers` | `agent/internal/update/`、`agentupdate.go`、`Servers.vue` | 已实现，真实主机验证 |
| 任务历史 | 状态/主机/时间筛选、分页、详情、日志回放、取消/重试/下载 | `/history` | `tasks.go`、`History.vue` | 已实现 |
| 可靠性 | 幂等键、进程组终止、超时、后端重启恢复、WS 断线重放 | — | `scheduler/state.go`、`ws/hub.go` | 已实现，含单元测试 |
| 审计 | 任务与资源操作记录（含操作用户名；子用户仅见自己的记录） | `/history` 审计接口 | `router.go` `audit()` | 已实现 |

接口路径与请求/响应格式见 [api.md](api.md)。

## 5. 文档地图

| 文档 | 内容 |
|---|---|
| [README.md](../README.md) | 项目介绍、快速开始、构建与测试 |
| [overview.md](overview.md) | 本文件：目录、配置、功能清单 |
| [architecture.md](architecture.md) | 组件、数据模型、状态机、恢复策略、演进设计 |
| [api.md](api.md) | 后端 API、Agent API、WebSocket 协议 |
| [deployment.md](deployment.md) | 安装、配置参考、安全清单、备份恢复、排障 |
| [progress.md](progress.md) | 已验证 / 待验证 / 已知边界 |

## 6. 已知限制

- 权限只有 admin/user 两级和按账号归属隔离，没有更细的 RBAC 或令牌 scope。
- 链路仍为明文 HTTP：Cookie、JWT、API 令牌都可能被内网抓包。
- 退出登录只撤销当前 Cookie 会话；已签发的 JWT 在到期前仍有效（改密可立即失效），
  API 令牌需在页面手动吊销。
- 输出按 300ms 轮询拉取；跨块 UTF-8 字符可能显示为 `�`。
- Agent 任务记录在内存中，Agent 重启后历史丢失（后端保守标 `UNKNOWN`）。
- 日志落本地磁盘，需自行清理；MinIO 只用于 Agent 制品，尚未承载任务日志。
- 单 MySQL、单后端，无高可用。
