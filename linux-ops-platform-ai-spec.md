# Linux 集中运维平台：AI 工程实施规范

> 版本：v1.0（第一阶段无认证）  
> 目标：让编码 AI 按阶段构建可实际部署、测试、维护的完整系统，而非演示项目。

## 0. 给编码 AI 的最高优先级指令

你是资深 Go/Linux/Vue2 全栈工程师及生产系统架构师。请在当前仓库实现本规范。**先检查仓库与运行环境、形成架构设计及实施计划，再分阶段编写、编译、测试、交付。** 不要生成只有页面或 Mock 接口的 Demo；不要把未实现或未测试的功能标记为完成。遇到影响架构的歧义，提出具体问题。每个阶段提交可运行的代码、配置、测试及文档，并记录实际验证结果。

**第一阶段明确取消全部认证功能**：不实现控制台登录、用户/角色/RBAC、JWT、API Token、Agent 认证、mTLS 客户端认证、证书鉴权、命令审批。前端到后端、后端到 Agent 的第一版业务接口不做身份认证。可保留未来认证的接口扩展点，但不得暗中启用认证。HTTPS 可作为可选传输加密（自签证书，不等于认证），HTTP 仅允许在明确受控的测试或内网环境使用。

**重要风险边界**：无认证的远程命令执行接口具有极高风险。第一阶段仅允许在受控、隔离的实验/管理网络内联调，禁止暴露公网或任何不受信任的网络。真正生产上线前必须补齐至少网络访问限制、Agent 身份认证、管理端访问控制与审计。不要把“无认证”误称为“生产安全”。不要提供绕过系统审计或隐藏执行痕迹的功能。

> 补充（2026-11 修订）：§18 监控改为**后端主动拉取**，复用 Agent 现有 19090 端口（新增只读 GET 接口），不新增监听端口、不引入 Agent 认证/密钥/证书。整条链路继续沿用 §0/§4 的「内网隔离 + 不做身份认证」约束，不设例外。详见 §19。

## 1. 项目目标

开发一个集中管理 Linux 主机的 Web 运维平台，支持：

1. Vue2 页面管理服务器列表、分组、标签、Agent 版本与在线状态。
2. 在页面选择一台或多台服务器，输入 Shell 命令，实时查看 stdout、stderr、退出码、耗时、执行状态。
3. Go 管理后端向目标 Linux 上的 Go Agent 下发任务；**所有目标主机上的命令由本机 Agent 执行**，不依赖 SSH。
4. 提供两种模式：非交互式批量命令，以及通过 PTY 的交互式终端（支持 cd、vim、top、Ctrl+C、窗口尺寸变化）。
5. MySQL 保存服务器、命令模板、任务元数据、执行历史；完整大量日志存文件或 MinIO，并在 MySQL 中保存索引。
6. MinIO 集中管理 Agent 安装/升级包；支持版本识别、分批升级、校验、健康检查与失败回滚。
7. Agent 提供可用 curl 调试的 HTTP API；前端通过管理后端 WebSocket/SSE 实时接收输出。
8. 内网离线部署，支持 Linux amd64/arm64，systemd 守护，无 Kubernetes 依赖。

## 2. 技术选型

| 层次 | 技术 | 说明 |
|---|---|---|
| 前端 | Vue 2.7、Element UI、Vue Router、Vuex、Axios | 不迁移 Vue3 |
| 终端 | xterm.js、WebSocket | xterm.js 负责渲染；PTY 负责真正交互 |
| 管理后端 | Go、Gin、sqlx 或 GORM | 优先单体服务，避免过度微服务化 |
| Agent | Go、os/exec、PTY 库 | 单二进制，amd64/arm64 |
| 数据库 | MySQL 8 | 服务器、模板、任务、升级元数据 |
| 对象存储 | MinIO | 安装包、升级包、可选历史日志 |
| 服务管理 | systemd | 管理后端、Agent、升级辅助程序 |
| 通信 | HTTP(S) JSON、SSE/WebSocket | 第一阶段无认证；保留后续加固空间 |

不要默认引入 Redis、消息队列、Kubernetes、Java 或 Spark。若确有必要，说明原因并先征得确认。

## 3. 架构与数据流

```text
浏览器 Vue2 + Element UI + xterm.js
             |
      HTTP(S) + WebSocket
             v
         Go 管理后端
        /      |       \
     MySQL    MinIO    任务调度/日志转发
                         |
                  HTTP(S) Agent API
                    /     |      \
                   v      v       v
                Agent A Agent B Agent N
                   |      |       |
              os/exec / PTY → Linux 本地命令
```

- 浏览器只访问管理后端，不直接连接 Agent。
- 管理后端负责服务器列表、任务创建、调度、并发限制、输出聚合、日志持久化和升级管理。
- Agent 负责在目标主机执行命令、流式输出、任务取消、运行状态上报和升级协作。
- 初期采用管理后端主动访问 Agent 的方式；明确需要管理后端到各 Agent 监听端口的网络连通性。如果不具备该条件，先讨论改为 Agent 主动连接，不得假设网络天然可达。
- Agent 不直接连接 MySQL；MinIO 下载方式（Agent 直连或后端代理）须根据实际网络拓扑决定。

## 4. 第一阶段认证与网络约束（必须严格执行）

### 本阶段不实现

- 控制台登录/注册、用户管理、角色管理、RBAC、JWT、Session 登录态。
- Agent API Token、客户端证书认证、mTLS、Agent 身份注册认证。
- 命令审批、操作人身份校验。

### 仍须实现的基础保护

- 后端与 Agent 的监听地址可配置，默认绑定 `127.0.0.1` 以防意外暴露；跨机器部署时由运维显式配置内网地址及网络 ACL。
- 提供防火墙/安全组/反向代理仅允许管理网段访问的部署说明；禁止默认绑定公网。
- 请求体、并发数、输出量、任务超时、脚本长度、WebSocket 连接数限制。
- 进程默认非 root；涉及特权命令必须有明确的系统级权限配置，不自动赋予无限 sudo。
- 记录任务 ID、时间、目标主机、命令、状态、退出码及错误信息；由于无用户认证，操作人字段标记为 `anonymous`/`unattributed`，不得伪造身份。
- 配置项预留后续认证中间件、Agent 身份、证书轮换扩展点。
- 自签 HTTPS 可作为可选配置，用于加密传输；**TLS 服务器证书校验不是对调用者的身份认证**。不允许把“启用 HTTPS”描述成已经限制了谁可以下发命令。

## 5. 服务器与 Agent 管理

服务器字段：ID、名称、IP、Agent 端口、主机名、OS、架构、分组、标签、Agent 版本、状态、最后心跳时间、创建/更新时间。

功能：新增/编辑/删除、搜索筛选、批量选择、健康检查、Agent 信息、心跳状态、离线提示、版本展示。心跳方式统一为后端定时探测（拉模式，见 §19）；监控快照拉取成功即视为心跳。

Agent 要求：Go 单二进制、配置文件、systemd、日志轮转、优雅退出、受控并发、运行任务清理、Linux amd64/arm64、版本信息、健康接口。避免以 root 默认运行。

## 6. 命令执行引擎

### 6.1 非交互式模式（优先交付）

- 使用 Go `os/exec` 执行 `/bin/bash -c` 或直接执行受控程序。
- 支持单行、多行脚本、管道、环境变量、工作目录、执行超时、stdout/stderr 分流、退出码。
- stdout/stderr 持续流式返回，不等待进程结束才返回。
- 每个任务使用唯一 ID；限制并发、输出大小和执行时长。
- 取消任务时正确终止进程组及子进程；防止僵尸进程。
- 批量任务有父任务 ID，每个目标服务器有独立子任务 ID、状态、日志、错误。
- 幂等处理：相同任务 ID 重复下发不得无意重复执行。

### 6.2 交互式 PTY 模式

- Agent 启动 PTY + 交互式 Shell；xterm.js 经管理后端 WebSocket 双向转发字节流。
- 支持连续会话、`cd` 状态保持、`top`、`vim`、Ctrl+C、终端尺寸变化、断线会话清理。
- 明确 PTY 与普通命令执行是不同引擎，不能以简单命令输出模拟完整终端。
- 会话有空闲超时、最大时长、输出与资源限制；连接断开后的保留/终止策略可配置。

### 6.3 Shell history 行为

- 非交互 `bash -c` 通常不自动写入 `~/.bash_history`。
- 交互式 Bash 可能维护并写入 history，即使是由 Go Agent 启动。
- 本项目默认**不依赖 Linux Shell history 作为任务记录**，统一使用管理平台任务日志与审计记录。
- 若要求交互式会话不写入目标用户的 Bash history，可提供**显式、可配置的会话级 history 设置**，不得修改或清空用户原有 `~/.bash_history`，不得删除系统审计、journald、auditd 或安全监控记录；必须在文档中说明不能保证系统其他日志无痕。

### 6.4 任务状态与恢复

状态：`PENDING`、`DISPATCHING`、`RUNNING`、`SUCCESS`、`FAILED`、`TIMEOUT`、`CANCELLED`、`UNKNOWN`。

必须处理：Agent 离线、后端重启、网络断开、浏览器刷新、重复提交、命令超时、日志流中断、取消失败。页面可重新查询历史日志并订阅运行中的任务。明确无法确认执行结果时标记 `UNKNOWN`，不能擅自标记成功。

## 7. Agent API 与实时传输

建议接口：

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/v1/health` | 健康检查 |
| GET | `/api/v1/info` | 主机与 Agent 信息 |
| POST | `/api/v1/tasks` | 创建非交互执行任务 |
| GET | `/api/v1/tasks/{id}` | 查询任务状态 |
| GET | `/api/v1/tasks/{id}/logs` | 流式日志/SSE 或按游标拉取 |
| POST | `/api/v1/tasks/{id}/cancel` | 取消任务 |
| GET | `/api/v1/version` | Agent 版本 |
| WebSocket | `/api/v1/terminal` | PTY 会话双向流 |

第一阶段 API 不做身份认证，但**不得公开暴露**。提供 `curl` 示例，包括提交任务、查询状态、流式读取日志、取消任务。HTTP/HTTPS 由配置决定；若启用自签 HTTPS，提供 CA 证书生成、SAN 配置与 `curl --cacert` 示例。不要默认使用 `curl -k`。

管理后端对前端提供独立 API；不将 Agent 地址直接暴露给浏览器作为调用入口。为前端 WebSocket 定义消息类型、任务 ID、目标节点 ID、序号、时间戳、stdout/stderr、状态、退出码和断线重放游标。

## 8. MySQL 命令模板库

- 命令分类、搜索、收藏、描述、创建/修改、版本历史、参数化模板、快捷执行、最近使用。
- 示例：磁盘、CPU、内存、进程、端口、systemd 服务、日志查询。
- 参数必须按类型校验，执行前展示最终命令；不能把不可信参数未经处理直接拼接为 Shell 代码。
- 第一阶段不做用户级收藏与权限审批，可采用全局收藏与全局模板。
- 记录每次模板执行对应的模板版本及实际参数（敏感参数须脱敏）。

## 9. MinIO Agent 安装包与集中升级

- 包字段：版本、OS、架构、对象路径、大小、SHA256、发布签名、上传时间、发布状态。
- 管理端支持上传、列表、校验、版本选择、指定目标主机、批次和并发数。
- 升级流程：选择版本 → 兼容性校验 → 创建升级任务 → 下载包 → 校验 SHA256 **及可信发布签名** → 备份旧版本 → 独立 updater/systemd 协作替换 → 重启 → 健康检查 → 成功或回滚。
- 包签名是软件完整性和发布来源校验，**不等同于 Agent API 调用者认证**。
- 支持灰度升级、暂停、失败记录、失败回滚、禁止重复并发升级；升级期间任务处理策略必须明确。
- 禁止未经校验直接执行 MinIO 下载的二进制；不要使用 `curl | sh` 作为升级机制。
- 如果 Agent 不能直连 MinIO，支持管理后端流式代理包下载，避免将 MinIO 凭证分发给 Agent。

## 10. 前端页面与 UI

采用现代、简洁、有科技感的 Vue2 + Element UI 后台：顶部一级导航、左侧二级导航、专业深色终端、清晰信息密度、响应式桌面布局。避免未经设计直接套用默认 Element UI 样式。

页面：

1. **系统概览**：总服务器、在线/离线、Agent 版本、任务成功率、近期任务。
2. **服务器管理**：列表、分组、标签、状态、编辑、批量选择、进入终端。
3. **命令执行**：目标选择、命令编辑、模板快速填充、并发参数、实时输出、批量汇总、逐机终端标签、取消与重试。
4. **交互终端**：xterm.js、单机 PTY、连接状态、尺寸调整、复制、清屏、断线提示。
5. **命令库**：分类、搜索、参数、编辑、收藏、快速执行。
6. **任务历史**：时间/服务器/状态筛选、日志详情、下载结果。
7. **Agent 管理**：在线状态、版本、升级包、批量升级、进度、失败回滚。
8. **系统配置**：MySQL/MinIO 连接配置说明、并发、超时、Agent 地址、TLS 传输设置；**无用户权限页面**。

所有按钮必须调用真实后端；错误、加载中、空状态、断线、部分失败都要有真实 UI 反馈。

## 11. 数据库与日志设计

至少包含：

- `servers`
- `server_groups`
- `server_tags`
- `agents`
- `command_templates`
- `command_template_versions`
- `execution_tasks`
- `execution_task_targets`
- `execution_log_indexes`
- `agent_packages`
- `agent_upgrade_tasks`
- `agent_upgrade_targets`
- `audit_events`

**第一阶段不创建** `sys_users`、`sys_roles`、`sys_permissions` 等用户认证表。

要求提供 DDL、迁移、索引、外键/约束、初始化示例、数据清理与归档策略。MySQL 保存元数据及日志索引，大量输出存文件或 MinIO；日志按任务与节点分段存储，支持游标续读。时间在后端统一用 UTC，前端本地化展示。

## 12. 仓库目录

```text
linux-ops-platform/
├── frontend/        # Vue2 + Element UI + xterm.js
├── backend/         # Go 管理后端
├── agent/           # Go Agent
├── updater/         # 独立升级辅助程序
├── database/        # DDL、迁移与初始化
├── configs/         # 配置样例，不含真实密钥
├── scripts/         # 构建、安装、卸载、证书脚本
├── deploy/          # systemd、反向代理配置
├── docs/            # 架构、API、运维、验收文档
├── tests/           # 单元、集成、E2E 测试
└── README.md
```

## 13. 部署与运维

- 支持内网离线构建或提供依赖预下载方案；Linux amd64/arm64 交叉编译。
- 提供 Agent 安装/卸载脚本、systemd unit、日志轮转、升级/回滚、管理端安装及启动说明。
- 配置文件包含监听地址、端口、MySQL、MinIO、超时、并发、日志目录、TLS 可选项。
- 配置中不硬编码数据库密码、MinIO 密钥、私钥；使用受限权限配置文件或环境变量。
- 列出端口、连通性要求、网络 ACL 示例、备份与恢复、排障指南。
- 明确：**无认证版本仅供隔离网络试运行，正式生产验收必须补安全加固阶段。**

## 14. 开发里程碑

### M0：架构与环境核查

检查现有仓库、Go/Node/MySQL/MinIO 可用性、目标 Linux 架构、网络连通方向；输出 `docs/architecture.md`、API 草案、数据库 ER/DDL 草案、状态机、升级方案、风险清单及可执行任务列表。此时先不要生成全部代码。

### M1：基础工程

创建前端、后端、Agent、数据库迁移、配置与 systemd；编译启动，打通后端到 Agent 健康检查。

### M2：最小真实执行闭环

浏览器选择一台服务器 → 后端创建任务 → Agent 执行 → stdout/stderr 实时返回 → 页面显示退出码 → MySQL 保存任务记录。提供 curl 测试。

### M3：批量执行与可靠性

多机并发、任务状态机、取消/超时、日志持久化、重连、断线恢复、失败重试。

### M4：交互终端

PTY + xterm.js + WebSocket，支持 cd、top、vim、Ctrl+C、窗口调整、会话生命周期管理；说明 Bash history 行为。

### M5：命令库与管理页面

服务器管理、命令模板、参数化、任务历史、仪表盘、UI 完善。

### M6：MinIO Agent 升级

包管理、版本校验、签名、分批升级、健康检查、回滚、升级日志。

### M7：测试与交付

执行自动化测试、端到端联调、异常场景验证、部署与运维文档。列出实际通过/失败/未执行的测试，不得虚报。

### M8：正式生产安全加固（单独阶段，第一版不实现）

上线前增加控制台访问控制、管理 API 认证、Agent 身份认证（建议 mTLS）、权限控制、证书轮换、网络隔离及安全审计强化。未完成 M8 时不得将系统标记为可安全暴露在生产网络。

## 15. 必须验证的测试

- 单机与多机真实执行；stdout/stderr 流式输出；退出码与耗时准确。
- 多行命令、管道、工作目录、超时、取消、进程组清理。
- Agent 离线、网络断开、后端重启、浏览器刷新、重复任务 ID。
- PTY 的 cd、top、vim、Ctrl+C、窗口尺寸变化、会话超时。
- 命令模板创建、参数校验、版本记录、历史查询与日志重放。
- MinIO 包上传、架构不匹配、哈希/签名错误拒绝、升级成功、升级失败回滚。
- 并发/输出限制、非 root 执行、网络访问限制、监听地址默认值。
- 若启用 HTTPS：自签 CA 的证书校验与 `curl --cacert` 联调。
- 检查第一版没有登录页面、用户表、JWT/Token/mTLS 等身份认证逻辑。

## 16. AI 每阶段工作规则与交付

1. 先列出当前阶段任务清单与验收条件。
2. 实现完整代码，不使用伪实现或永久 Mock。
3. 运行格式化、静态检查、单元/集成测试及真实编译；记录命令和结果。
4. 修复失败项，不能静默跳过测试。
5. 更新 `README.md`、`docs/architecture.md`、`docs/progress.md`。
6. 报告已完成、未完成、已验证、未验证、已知风险和下一阶段计划。
7. 除非用户明确要求，禁止自行扩大技术栈、改变 Vue2、引入复杂基础设施。
8. 不得以“关闭认证”为理由关闭系统审计、日志或基本资源限制。

## 17. 现在开始执行的指令

**请先执行 M0：检查当前项目和环境，输出完整架构方案、通信方向、数据库模型、API 草案、执行状态机、PTY 方案、MinIO 升级与回滚方案、分阶段任务清单。第一版明确不做任何身份认证，但须标注隔离环境限制和正式生产前的安全补齐要求。等我确认架构后，再逐阶段编写真实可运行的系统。**


## 18. 新增功能：服务器监控与硬件健康（v2）

基于已完成的功能，需要新增以下能力。

一个类似 Node Exporter + Prometheus + Grafana 的轻量级服务器监控平台，同时增强物理服务器硬件监控能力，尤其是 SMART 磁盘健康、RAID 控制器自动发现、RAID 级别识别、阵列降级检测、故障硬盘槽位定位等。

系统必须具备：

Linux Go Agent 自动采集服务器指标，并在本地缓存最新快照。
后端复用现有 19090 端口主动拉取指标，Agent 不主动外连。
Go Server 集成到现有 agentclient/调度器中，实现集中拉取、存储、分析监控数据。
Vue2 + Element UI Web 管理控制台。
ECharts 实时与历史监控曲线。
自动识别服务器厂商、型号、操作系统、硬件信息。
自动识别 RAID 控制器并选择合适的采集方式。
支持软件 RAID、硬件 RAID、ZFS、LVM RAID、HBA/JBOD。
支持 SMART 硬盘健康与故障预警。
支持告警、历史数据、主机管理、Agent 管理。
支持 Linux x86_64 和 aarch64。
支持长期稳定运行及生产部署。

页面风格：
简洁、现代、专业、有科技感。
采用浅色为默认主题，预留深色模式。
不要使用过度发光、渐变、大面积高饱和颜色或复杂装饰。

核心设计要求
后端主动拉取：复用 Agent 19090 端口，不新增监听端口。
Server 不需要 SSH 到被监控服务器，只通过 Agent HTTP 接口取数。
Agent 不主动外连监控中心，不需要注册、密钥或证书。
支持多台服务器并发拉取（有界并发 + 错峰抖动 + 单机超时）。
支持 gzip 压缩响应。
拉取失败按退避重试；连续失败判定主机离线。
Agent 本地保留短期环形缓冲（建议 10–15 分钟），供后端重启后补采。
支持 Agent 版本管理（复用现有 /info 与在线升级）。
Agent 心跳由成功拉取隐含，不单设心跳接口。
支持 Agent 自身健康监控（采集器状态、耗时、内存）。

三、Agent 基础信息采集

3.1 操作系统

采集：

主机名

Machine ID（使用哈希或内部标识）

操作系统发行版

操作系统版本

Linux 内核版本

CPU 架构

系统启动时间

系统运行时间

Agent 版本

Agent 启动时间

优先读取：

/etc/os-release
/proc/uptime
/proc/version
/sys/class/dmi/id/

3.2 服务器硬件信息

采集：

服务器厂商

服务器型号

BIOS 版本

主板型号

CPU 型号

CPU 物理数量

CPU 核心数量

逻辑 CPU 数量

NUMA 节点

物理内存总量

服务器序列号属于敏感资产信息，默认仅保存必要标识，并限制 Web 访问权限。

五、CPU 监控

采集：

CPU 总使用率

User

System

Idle

IOWait

IRQ

SoftIRQ

Steal

每核心使用率

Load 1/5/15

上下文切换

中断数量

进程数量

线程数量

采集来源：

/proc/stat
/proc/loadavg
/proc/cpuinfo

要求：

通过累计计数器差值计算使用率。

正确处理 CPU 热插拔和计数器重置。

支持 128 核及以上服务器。

每核心指标允许单独开启或关闭。

Web 展示实时使用率、历史曲线和 CPU 核心热力图。

六、内存监控

采集：

MemTotal

MemAvailable

MemFree

Buffers

Cached

SwapTotal

SwapFree

Dirty

Writeback

Active

Inactive

Slab

CommitLimit

Committed_AS

内存使用率优先使用：

(MemTotal - MemAvailable) / MemTotal * 100

Web 展示：

内存总量

已使用内存

可用内存

Cache

Swap

内存使用率曲线

七、磁盘 I/O 监控

采集所有真实块设备。

指标：

读取 Bytes/s

写入 Bytes/s

Read IOPS

Write IOPS

平均读取延迟

平均写入延迟

I/O Busy Time

Weighted I/O Time

当前 In-flight I/O

磁盘设备型号

容量

设备类型

采集来源：

/proc/diskstats
/sys/block/

要求：

通过采样差值计算速率。

使用实际采样时间计算。

正确处理计数器重置。

避免重复汇总物理盘、分区、dm、md 设备造成统计翻倍。

支持 HDD、SSD、NVMe、RAID 虚拟磁盘。

支持设备热插拔。

支持设备筛选。

磁盘 I/O 利用率必须明确说明是 busy time 估算，不能等同于设备性能饱和率。

Web 曲线：

读写 MB/s

IOPS

读写延迟

I/O Busy

队列情况

八、文件系统容量

采集：

挂载点

文件系统类型

总容量

已用容量

可用容量

使用率

inode 总数

inode 使用率

只读状态

支持：

ext4

xfs

btrfs

NFS

HDFS 挂载目录（如果存在）

其他 Linux 文件系统

要求：

过滤不必要的伪文件系统。

不阻塞在失效的远程挂载点上。

支持按挂载点配置排除规则。

展示挂载点与底层设备关系。

九、网络监控

9.1 网卡

采集每张网卡：

名称

MAC

MTU

链路状态

速率

双工模式

RX Bytes/s

TX Bytes/s

RX Packets/s

TX Packets/s

RX Drops

TX Drops

RX Errors

TX Errors

支持：

物理网卡

Bond

VLAN

Bridge

虚拟网卡

避免物理网卡和 Bond 的流量重复汇总。

9.2 Bond / LACP

自动检测：

/proc/net/bonding/

采集：

Bond 名称

Bond 模式

Slave 成员

Active Slave

MII Status

链路速率

LACP 状态

成员链路异常

9.3 TCP 连接

采集：

ESTABLISHED

LISTEN

TIME_WAIT

CLOSE_WAIT

SYN_SENT

SYN_RECV

FIN_WAIT

TCP Retransmits

TCP OutSegs

TCP InSegs

优先使用内核接口和 Netlink。

不要每 5 秒启动一次 ss -ant 扫描所有连接。

Web 展示：

当前连接数

TCP 状态分布

历史连接曲线

TCP 重传率

网络收发趋势

十、SMART 硬盘健康监控

10.1 自动发现

Agent 自动识别：

SATA

SAS

NVMe

RAID 控制器后面的物理盘

HBA/JBOD 物理盘

优先使用 sysfs 发现设备。

根据设备类型选择 smartctl 采集参数。

对于硬件 RAID 后面的磁盘，允许通过控制器工具获取设备映射，再使用适当的 smartctl 访问方式。

10.2 采集指标

磁盘型号

序列号

固件版本

容量

接口类型

SMART 总体状态

温度

通电小时数

重映射扇区

待映射扇区

不可纠正错误

NVMe Percentage Used

NVMe Available Spare

NVMe Media Errors

要求：

优先解析 smartctl JSON 输出。

兼容不同 smartctl 版本。

不同接口使用不同指标映射。

SMART 状态区分 Healthy、Warning、Failed、Unknown、Unsupported。

不能因工具缺失而报告 Healthy。

支持自定义 SMART 阈值。

支持记录历史健康变化。

十一、RAID 自动发现与自适应采集引擎

这是整个系统的核心功能。

模块名称：

RAID Auto Discovery Engine

11.1 自动识别目标

Agent 启动后自动完成：

识别服务器厂商。

识别服务器型号。

识别操作系统和 CPU 架构。

扫描 PCI RAID/SAS/SATA/NVMe 控制器。

识别 PCI Vendor ID、Device ID、Subsystem ID。

识别内核驱动。

检测软件 RAID。

检测硬件 RAID。

检测 ZFS 和 LVM RAID。

检测 HBA/JBOD。

匹配适配器。

采集控制器、阵列、物理盘状态。

不能仅根据服务器型号判断 RAID 类型。

同一台服务器允许存在多个 RAID 控制器和多种存储技术。

11.2 预置适配器

至少设计以下适配器：

适配器

类型

采集工具

LinuxMDAdapter

Linux 软件 RAID

mdadm

MegaRAIDAdapter

LSI/Broadcom

StorCLI

DellPERCAdapter

Dell PERC

perccli / StorCLI

HPESmartArrayAdapter

HPE Smart Array

ssacli

AdaptecAdapter

Microchip/Adaptec

arcconf

IntelVROCAdapter

Intel VROC / IMSM

mdadm / 厂商工具

ZFSAdapter

ZFS

zpool

LVMRAIDAdapter

LVM RAID

lvs

HBAAdapter

HBA/JBOD

sysfs / smartctl

注意：

HBA/JBOD 不一定存在 RAID 阵列。

不同适配器需要区分控制器、逻辑卷、存储池和物理磁盘。

实际识别必须综合 PCI ID、驱动、控制器型号、工具检测结果及设备拓扑。

支持：

自动匹配

多适配器并行发现

工具版本检测

工具路径配置

架构兼容性检测

规则版本管理

未知控制器诊断

11.3 RAID 指标

控制器：

厂商

型号

PCI 地址

驱动

固件版本

控制器健康状态

虚拟磁盘或阵列：

RAID 级别

阵列容量

阵列状态

物理盘数量

重建进度

热备状态

物理磁盘：

Enclosure

Slot

Device ID

序列号

型号

容量

介质类型

Online / Failed / Rebuilding / Hot Spare

SMART 状态

温度

11.4 状态标准化

统一状态：

Optimal
Degraded
Failed
Rebuilding
Unknown
Unsupported

同时保留厂商原始状态。

采集状态单独定义：

Success
PartialSuccess
Failed
ToolMissing
PermissionDenied
Timeout
Unsupported

采集失败不等于 RAID 正常。

11.5 故障盘定位

支持以下层级：

Server
  └── RAID Controller
       └── Virtual Disk / Array
            └── Physical Disk
                 ├── Enclosure
                 ├── Slot
                 ├── Serial Number
                 ├── Model
                 └── Health

当 RAID 降级时，页面尽可能明确展示：

哪台服务器

哪个控制器

哪个阵列

哪个 Enclosure

哪个 Slot

哪块物理盘

当前故障状态

只有获得可靠背板映射时，才允许显示机箱物理槽位位置。

11.6 工具管理

Agent 自动检测本机工具：

smartctl

mdadm

storcli

perccli

ssacli

arcconf

zpool

lvs

如果缺少工具：

报告缺失状态

显示推荐工具

显示 CPU 架构

显示工具兼容性

不自动下载未知程序

不自动执行未经授权的安装

厂商工具必须遵守其许可与再分发要求。

11.7 安全

所有硬件采集命令必须：

使用 exec.CommandContext

设置超时

限制输出大小

使用固定白名单命令

不使用 Shell 拼接

不接受任意远程命令

不允许执行 RAID 创建、删除、重建、清除配置等修改操作

采用最小权限

记录采集错误

十二、采集周期

默认配置：

采集任务

周期

CPU

5 秒

内存

5 秒

Load

5 秒

网络

5 秒

TCP

5 秒

磁盘 I/O

5 秒

文件系统容量

30 秒

RAID

60 秒

SMART

300 秒

硬件基本信息

启动时及每小时

Agent 自检（自身健康）

10 秒

所有周期允许配置。

后端拉取周期独立配置，默认 5 秒；慢采集组（RAID/SMART/硬件）由 Agent 内部按上表刷新，`/metrics` 只返回缓存。

不同采集器独立运行，慢速 SMART 或 RAID 采集不能阻塞 CPU、网络等基础指标。

十三、Agent 指标接口（拉模式）

后端通过 Agent 现有 HTTP 端口（默认 19090）主动拉取：

GET /metrics                     最新快照（读本地缓存，毫秒级返回）
GET /metrics?groups=cpu,mem      只取指定采集组（可选）
GET /metrics/history?since_ms=…  环形缓冲补采（可选）

要求：

Agent 侧采集器按各自周期独立运行，/metrics 只读缓存，不在请求内执行 smartctl / RAID 命令

响应支持 gzip，单次建议 < 2 MB

快照中每个采集组带 collected_at_ms 与 status，后端据此判断新鲜度与失败原因

后端有界并发拉取，单机超时默认 3 秒，失败不影响其它主机

时序时间戳由后端拉取时刻决定，不依赖 Agent 时钟

连续 3 次拉取失败 → 主机标记离线并产生采集告警；恢复后自动继续

不需要注册/心跳接口、批次序号、幂等键、离线缓存、Agent 密钥或证书

复用现有约束：Agent 仅监听内网/回环，禁止暴露公网

十四、数据库设计

MySQL

建议包含：

hosts
host_groups
agents
agent_versions
hardware_inventory
network_interfaces
block_devices
filesystems
raid_controllers
raid_arrays
raid_physical_disks
smart_devices
alert_rules
alert_events
alert_notifications
collection_errors
system_settings

要求：

合理建立索引

支持历史硬件变化

支持设备热插拔

支持软删除

支持配置版本

支持数据迁移

VictoriaMetrics

存储：

CPU

内存

网络

TCP

磁盘 I/O

文件系统容量

SMART 数值指标

RAID 状态指标

Agent 自身健康指标

要求：

使用稳定标签

控制标签基数

避免把错误文本、时间戳等作为标签

支持历史查询

支持聚合查询

支持不同时间范围的查询步长

支持数据保留策略

十五、Web 管理控制台

15.1 整体风格

Vue2 + Element UI。

采用：

顶部导航

左侧二级菜单

主内容区域

清晰的数据卡片

专业表格

ECharts 曲线

统一图标

响应式布局

风格要求：

简洁、精致、现代、科技感。

不要设计成传统老旧运维后台。

15.2 首页：监控总览

显示：

服务器总数

在线数量

离线数量

异常数量

当前告警数量

CPU 平均使用率

内存平均使用率

总网络吞吐

RAID 降级数量

SMART 异常硬盘数量

支持主机列表筛选。

15.3 服务器列表

列包括：

主机名

IP

服务器厂商

型号

OS

CPU 使用率

内存使用率

磁盘使用率

网络速率

RAID 状态

SMART 状态

Agent 状态

最后成功采集时间

支持：

搜索

排序

分页

分组

批量筛选

15.4 服务器详情

Tab：

性能总览

CPU

内存

磁盘 I/O

文件系统

网络

TCP 连接

SMART

RAID

硬件信息

告警

Agent 诊断

15.5 曲线

使用 ECharts。

支持：

实时刷新

1 小时

6 小时

24 小时

7 天

30 天

自定义时间范围

多指标叠加

单位自动转换

鼠标悬停提示

缩放

导出 PNG

CSV 导出

不同量纲的指标不能错误地使用同一个纵轴。

15.6 RAID 页面

显示：

服务器型号

RAID 控制器

RAID 级别

虚拟磁盘

阵列状态

物理磁盘槽位

SMART 健康

重建进度

热备盘

采集工具

最近采集时间

采集错误

需要有直观的磁盘槽位视图。

颜色：

绿色：正常

黄色：警告、重建

红色：故障、降级

灰色：未知、未采集

同时使用文字和图标区分状态，不允许只依赖颜色。

15.7 RAID 自动发现诊断

新增页面：

RAID Auto Discovery

显示：

服务器厂商与型号

PCI 控制器

Vendor ID / Device ID

内核驱动

匹配的适配器

采集工具路径

工具版本

最近采集耗时

最近成功时间

采集状态

错误原因

十六、告警系统

支持：

CPU 使用率过高

内存不足

磁盘空间不足

inode 不足

磁盘 I/O 延迟过高

网络丢包

TCP 重传异常

Bond 成员掉线

服务器离线

Agent 异常

SMART 健康异常

RAID 降级

RAID 故障

RAID 重建

物理磁盘故障

RAID 采集失败

要求：
阈值配置
持续时间
告警恢复
告警去重
告警抑制
告警历史
严重级别
主机级规则
分组级规则
十七、Agent 性能要求
Agent 必须适合长期运行。

目标：
常规采集 CPU 开销尽量低于单个逻辑核心的 1%，实际以基准测试为准。
常规 RSS 尽量控制在 50MB 左右，视设备数量和采集器而定。
采集器独立调度。
SMART、RAID 外部命令不得高频执行。
支持 128 核及以上服务器。
支持数十块物理磁盘。
支持多网卡和 Bond。
支持 x86_64、aarch64。
支持 systemd 自动启动。
支持异常退出自动恢复。
支持优雅停止。
支持日志轮转。

## 十九、实现边界与关键决策（执行前确认）

以下约定用于解决 §18 与既有实现、旧版规格之间的冲突。未确认前不进入编码。

### 19.1 与现有系统的关系

- 现有 Agent 19090 端口的执行、文件、终端、升级能力保持不变。
- 监控复用同一端口，新增只读 GET 接口；不新开监听端口。
- Agent 仍然不主动连接后端；后端是唯一发起方，网络方向与现有执行链路一致，防火墙只需放行「后端 → 19090」。
- §18 中原「Agent 主动上报 / 不开放端口」的要求作废，改为「复用既有端口拉取」。
- §0/§4 的「不做身份认证」继续适用于整条链路，不新增 Agent 认证例外。

### 19.2 认证与 TLS（不引入）

- 拉模式下没有「谁在推数据」的问题：数据来源由后端拨号的目标地址（`servers.ip`）决定，无法被伪造成另一台主机。
- 因此不需要注册令牌、agent_id、长期密钥、HMAC 签名、时间偏移校验，也不需要为监控单独分发 CA。
- 沿用现状：HTTP 明文、仅限内网、禁止公网暴露。未来加密与执行链路统一升级（M8），监控不单独做。
- 风险须在文档写明：19090 本身无认证，能访问它的人既能执行命令也能读指标；监控数据敏感度不高于执行能力，因此不构成新增风险面。

### 19.3 组件与数据流

- MySQL：资产、配置、告警规则/事件、采集错误、版本记录。
- VictoriaMetrics（单节点）：时序指标，由后端写入，Web 经后端查询。
- MinIO：继续存放 Agent 制品，不承载监控数据。
- 采集链路：后端 Scraper（定时 + 有界并发）→ `GET http://<ip>:19090/metrics` → 校验/补标签 → 写 VictoriaMetrics。
- 查询链路：Web → 后端 → VictoriaMetrics / MySQL。
- 备选（规模化后再考虑）：由 vmagent 直接抓取 `/metrics`，后端只维护 targets；v2 不引入，避免多一套配置与标签注入逻辑。

### 19.4 现有表复用（不新建并行主机表）

| 新需求表 | 落地方式 |
|---|---|
| hosts | 复用 `servers`，按需扩展硬件字段 |
| host_groups | 复用 `groups` |
| agents | 复用 `agents`，扩展最后成功拉取时间、采集器状态 |
| agent_versions | 新建 |
| hardware_inventory / network_interfaces / block_devices / filesystems / raid_* / smart_devices | 新建，统一按 `server_id` 关联 `servers.id` |
| alert_rules / alert_events / alert_notifications / collection_errors / system_settings | 新建 |

历史变化（换盘、换网卡、RAID 重构）使用 `*_history` 表或 `valid_from/valid_to` 记录，不覆盖旧记录。

### 19.5 拉取协议与快照格式

后端按周期 `GET /metrics`，Agent 返回本地最新快照（不在请求内做慢采集）：

```json
GET /metrics   (Accept-Encoding: gzip)
{
  "agent_version": "0.2.0",
  "machine_id": "sha256:...",
  "collected_at_ms": 1730000000000,
  "collectors": {
    "cpu":      { "collected_at_ms": 1730000000000, "status": "Success", "data": { "usage_percent": 12.5, "per_core": [12.1, 13.0], "load1": 0.4 } },
    "memory":   { "collected_at_ms": 1730000000000, "status": "Success", "data": { "total_bytes": 0, "available_bytes": 0, "swap_used_bytes": 0 } },
    "disk":     { "collected_at_ms": 1730000000000, "status": "Success", "data": [ { "device": "nvme0n1", "read_bps": 0, "write_bps": 0, "iops": 0 } ] },
    "fs":       { "collected_at_ms": 1730000000000, "status": "Success", "data": [ { "mount": "/", "used_percent": 61.2, "inode_used_percent": 3.1 } ] },
    "net":      { "collected_at_ms": 1730000000000, "status": "Success", "data": [ { "device": "eth0", "rx_bps": 0, "tx_bps": 0, "drops": 0 } ] },
    "tcp":      { "collected_at_ms": 1730000000000, "status": "Success", "data": { "established": 42, "time_wait": 8, "retrans_rate": 0.001 } },
    "hardware": { "collected_at_ms": 1729999000000, "status": "Success", "data": { } },
    "raid":     { "collected_at_ms": 1729998000000, "status": "PartialSuccess", "data": { } },
    "smart":    { "collected_at_ms": 1729997000000, "status": "ToolMissing", "data": null }
  },
  "agent_self": { "goroutines": 18, "rss_bytes": 0, "collect_duration_ms": { "cpu": 4, "smart": 812 } }
}
```

- 每个采集组自带 `collected_at_ms` 与 `status`，后端据此判断新鲜度与失败原因。
- 慢采集组（hardware/raid/smart）在 Agent 内部按 60s/300s 独立刷新，`/metrics` 只读缓存，保证拉取延迟稳定。
- 后端每次拉取写入整份快照；相同时间戳的重复写入由 VictoriaMetrics 幂等去重。
- 后端记录每台主机的 `last_ok_at` 与连续失败次数；连续 3 次失败标记离线并告警。
- 环形缓冲建议保留 10–15 分钟（5s 采样约 120–180 份），后端重启后通过 `/metrics/history?since_ms=` 补采；超出窗口的数据记为缺口。
- 所有时间戳为 UTC 毫秒。

### 19.6 告警通知范围（v1）

- 渠道：站内告警中心 + Webhook（通用 JSON，可对接企业微信/钉钉机器人）；邮件/短信后续。
- 规则字段：`metric`、`operator`、`threshold`、`duration`、`severity`、`scope`（host/group/global）、`enabled`、`silence_until`。
- 去重键：`(rule_id, host_id, device_id)`；状态：firing → acknowledged → resolved；恢复需连续满足阈值持续时间。
- RAID/SMART 采集失败本身必须告警（采集失败 ≠ 正常）。

### 19.7 规模与性能基线（可配置）

- 单后端 ≤ 500 台主机；基础指标 5s；RAID 60s；SMART 300s。
- 拉取并发默认 32（可配），单机超时 3s，按 `server_id` 取模错峰；500 台 × 5s ≈ 100 次 GET/s，使用 keep-alive 复用连接。
- 每主机 ≤ 64 块物理盘、≤ 8 个 RAID 控制器、≤ 32 张网卡、≤ 128 逻辑核。
- VictoriaMetrics 原始精度保留 30 天，降采样保留 180 天（按部署调整）。
- Agent：常规采集 CPU 开销 < 1 个逻辑核，RSS 目标约 50MB；慢采集独立 goroutine，禁止阻塞基础采集。
- 后端宕机期间无采集，靠 Agent 环形缓冲补采；超出缓冲窗口的数据丢弃并记录缺口。

### 19.8 安全底线

- 外部命令使用固定白名单 + `exec.CommandContext` + 超时 + 输出上限，禁止 shell 拼接。
- 只读采集；禁止 RAID 创建/删除/重建/清配置等写操作。
- 不自动下载或安装厂商工具，只报告缺失与推荐。
- 不新增端口；沿用 19090 仅监听内网/回环、禁止暴露公网的约束。
