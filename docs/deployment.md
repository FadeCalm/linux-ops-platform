# 部署与运维手册（M1–M3）

目标环境：**单台 Linux amd64 服务器**，可访问外网（用于安装 Go/Node/Docker 镜像）。
后端、Agent、MySQL、MinIO 全部部署在这台服务器上；Agent 同时充当第一台受管主机。

> **安全警告（务必先读）**
> 第一阶段**没有认证**。任何能访问 8080 / 19090 端口的人都能执行任意命令。
> - 默认只监听 `127.0.0.1`
> - 跨机访问必须显式绑定**内网地址**，并用防火墙/安全组限制到管理网段
> - 严禁暴露到公网；不要把它描述为「生产安全」
> - 正式生产前必须先完成 M8 安全加固

---

## 1. 前置条件

| 项 | 版本 / 说明 |
|---|---|
| 操作系统 | Linux amd64（Ubuntu 22.04 / Debian 12 / Rocky 9 等） |
| Go | 1.22+ |
| Node.js / npm | 18+（构建前端） |
| Docker + Compose 插件 | 运行 MySQL 8 与 MinIO |
| 系统用户 | installer 会创建 `linuxops`（非 root，`nologin`） |
| 磁盘 | 预留日志空间；输出上限默认 10 MiB/路/任务 |

安装 Go/Node 的方式不在本手册范围内，请使用发行版包管理器或官方安装包，
**不要使用 `curl | sh` 这类无法审计的安装方式**。

## 2. 端口与网络

| 端口 | 组件 | 默认绑定 | 用途 |
|---|---|---|---|
| 8080 | 后端 | `127.0.0.1` | REST + WebSocket + 静态前端 |
| 19090 | Agent | `127.0.0.1` | 被后端调用 |
| 3306 | MySQL | `127.0.0.1`（docker） | 元数据 |
| 9000/9001 | MinIO | `127.0.0.1`（docker） | M6 才使用 |

跨主机部署时：

- 如果后端在机器 A、Agent 在机器 B，Agent 必须监听 B 的内网地址
  （例如 `10.0.0.11`），并且 A→B:19090 放行
- 浏览器在管理网段时，后端监听内网地址，且只放行管理网段到 8080
- 后端与 Agent 之间的流量目前是 **HTTP 明文**；只能在隔离网络使用

## 3. 快速安装（推荐）

### 3.1 获取代码

把仓库放到服务器，例如 `/root/linux-ops-platform`。

### 3.2 准备密钥

```bash
cd linux-ops-platform/deploy
cp .env.example .env
# 编辑 .env，设置强密码
```

建议使用十六进制密码，避免 DSN 特殊字符问题：

```bash
openssl rand -hex 16
```

`.env` 字段：

| 变量 | 说明 |
|---|---|
| `MYSQL_ROOT_PASSWORD` | MySQL root 密码 |
| `DB_NAME` | 默认 `linux_ops` |
| `DB_USER` | 默认 `ops` |
| `DB_PASSWORD` | 后端使用的密码（与 MySQL 用户一致） |
| `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` | M6 使用，现在也要求设置 |

`deploy/.env` 必须 `chmod 600`，且不要提交到版本库。

### 3.3 执行安装

```bash
sudo bash scripts/install.sh --lan-ip 10.0.0.10
```

- 不带 `--lan-ip`：后端只监听 `127.0.0.1:8080`，仅本机可访问
- 带 `--lan-ip`：后端监听 `<私网IP>:8080`，请立即在防火墙限制该端口
- 已有外部 MySQL 时可加 `--skip-compose`，并手工配置 `DB_DSN`/连接参数

installer 会：

1. 检查 Go/Docker；启动了 MySQL 8 与 MinIO（`docker compose up -d`）
2. 构建 backend、agent、frontend
3. 创建 `linuxops` 用户与目录
   - `/opt/linux-ops-platform/bin`：二进制
   - `/opt/linux-ops-platform/frontend`：前端静态文件
   - `/etc/linux-ops/backend.yaml`、`agent.yaml`
   - `/etc/linux-ops/backend.env`（600，含 `DB_PASSWORD`、`LISTEN_ADDR`、`LOG_DIR`、`FRONTEND_DIR`）
   - `/var/lib/linux-ops/logs`：任务日志
4. 安装并启动 `linux-ops-agent`、`linux-ops-backend` systemd 服务
5. 等待 `/api/v1/health` 通过

### 3.4 验证

```bash
systemctl status linux-ops-backend linux-ops-agent --no-pager
curl -sS http://127.0.0.1:8080/api/v1/health
curl -sS http://127.0.0.1:19090/api/v1/info
sudo bash tests/smoke.sh
```

`tests/smoke.sh` 会依次验证：后端健康 → 选择服务器 → Agent 在线 →
提交真实任务 → 轮询状态 → 读取 stdout/stderr 内容。需要 `curl` 与 `python3`。

浏览器访问：

- 本机：`http://127.0.0.1:8080/`
- 管理网段：`http://<LAN_IP>:8080/`（前提是 `LISTEN_ADDR` 已设为该地址）

## 4. 手工部署（可选）

```bash
# 1) 基础设施
cd deploy && docker compose up -d && cd ..

# 2) 构建（约 3–10 分钟）
bash scripts/build.sh

# 3) 用户与目录
sudo useradd --system --home /opt/linux-ops-platform --shell /sbin/nologin linuxops || true
sudo install -d -m 0755 /opt/linux-ops-platform/{bin,frontend,docs}
sudo install -d -m 0750 -o linuxops -g linuxops /var/lib/linux-ops/logs
sudo install -d -m 0755 /etc/linux-ops

# 4) 二进制与前端
sudo install -m 0755 bin/backend /opt/linux-ops-platform/bin/backend
sudo install -m 0755 bin/agent   /opt/linux-ops-platform/bin/agent
sudo cp -r frontend/dist/. /opt/linux-ops-platform/frontend/

# 5) 配置
sudo install -m 0644 configs/backend.yaml /etc/linux-ops/backend.yaml
sudo install -m 0644 configs/agent.yaml   /etc/linux-ops/agent.yaml
sudo install -m 0600 /dev/null /etc/linux-ops/backend.env
# 写入 DB_PASSWORD / LOG_DIR / FRONTEND_DIR / LISTEN_ADDR

# 6) systemd
sudo install -m 0644 deploy/systemd/*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now linux-ops-agent linux-ops-backend
```

数据库二选一：

- 保持 `mysql.auto_migrate: true`（默认），后端启动时自动建表
- 或手工执行 `database/schema.sql`，并把 `auto_migrate` 设为 `false`

## 5. 配置参考

### 5.1 后端（`/etc/linux-ops/backend.yaml`）

| 键 | 默认 | 说明 |
|---|---|---|
| `listen_addr` | `127.0.0.1:8080` | 监听地址 |
| `allow_origin` | `*` | CORS；生产建议固定为前端 origin |
| `frontend_dir` | 空 | 指向构建后的前端目录；空则只提供 API |
| `log_dir` | `./data/logs` | 任务日志根目录 |
| `mysql.host/port/user/dbname` | 见样例 | 连接参数 |
| `mysql.password` | 空 | **用环境变量 `DB_PASSWORD`** |
| `mysql.dsn` | 空 | 完整 DSN，优先级最高；用 `DB_DSN` 注入 |
| `mysql.auto_migrate` | `true` | 自动建表 |
| `mysql.seed` | `true` | 空库时插入 `local-agent` 示例 |
| `scheduler.heartbeat_interval_sec` | 20 | 心跳间隔 |
| `scheduler.dispatch_concurrency` | 8 | 全局并发派发数 |
| `scheduler.poll_interval_ms` | 300 | 拉日志间隔 |
| `scheduler.default_timeout_ms` | 300000 | 默认超时 |
| `scheduler.max_timeout_ms` | 3600000 | 超时上限 |
| `scheduler.max_script_bytes` | 65536 | 命令长度上限 |
| `scheduler.max_output_bytes` | 10485760 | 单路输出上限（展示用） |
| `scheduler.agent_request_timeout_sec` | 10 | 调用 Agent 的 HTTP 超时 |
| `websocket.max_connections` | 200 | WS 连接上限 |
| `websocket.replay_buffer_len` | 2000 | 每目标重放环形缓冲帧数 |

环境变量覆盖：`LISTEN_ADDR`、`DB_PASSWORD`、`DB_DSN`、`DB_HOST`、`DB_PORT`、
`DB_USER`、`DB_NAME`、`LOG_DIR`、`ALLOW_ORIGIN`、`FRONTEND_DIR`。

### 5.2 Agent（`/etc/linux-ops/agent.yaml`）

| 键 | 默认 | 说明 |
|---|---|---|
| `listen_addr` | `127.0.0.1` | 跨机时必须改为内网地址 |
| `listen_port` | 19090 | |
| `work_dir` | `/tmp` | 命令默认工作目录 |
| `max_concurrent` | 8 | 本机并发执行上限 |
| `max_output_bytes` | 10485760 | 每路输出缓冲上限，超出截断 |
| `max_script_bytes` | 1048576 | 命令长度上限 |
| `default_timeout_ms` | 300000 | 默认超时 |
| `max_timeout_ms` | 3600000 | 超时上限 |
| `task_retention_sec` | 3600 | 已结束任务在内存中保留时长 |

环境变量：`AGENT_LISTEN_ADDR`、`AGENT_LISTEN_PORT`、`AGENT_LOG_DIR`。

### 5.3 systemd 沙箱的影响

两个 unit 都启用了 `NoNewPrivileges`、`ProtectSystem=full`、`ProtectHome=true`、
`PrivateTmp=true`。实际含义：

- 命令**不能**访问 `/home`、`/root`
- Agent 看到的 `/tmp` 是服务私有命名空间，与系统 `/tmp` 不共享
- 需要持久化工作目录时，请把 `work_dir` 指向 `/var/lib/linux-ops` 下的子目录，
  并确保 `linuxops` 用户可写
- Agent 以 `linuxops` 运行，默认不能执行特权命令；如确需，必须在
  `/etc/sudoers.d/` 添加**窄范围**规则，绝不要 `NOPASSWD: ALL`

## 6. 增加受管主机

在另一台 Linux 主机上：

```bash
# 复制 agent 二进制与配置
sudo install -d /opt/linux-ops-agent /etc/linux-ops
sudo install -m 0755 bin/agent /opt/linux-ops-agent/agent
sudo install -m 0644 configs/agent.yaml /etc/linux-ops/agent.yaml
```

修改 `/etc/linux-ops/agent.yaml`：

```yaml
listen_addr: "10.0.0.11"   # 该主机的内网地址
```

用 systemd 运行（可复用 `deploy/systemd/linux-ops-agent.service`，
调整路径），然后：

1. 在平台「服务器管理」中新增该主机：IP `10.0.0.11`，端口 `19090`
2. 点「健康检查」，状态应变为 `online`
3. 防火墙只放行后端所在主机到 19090

## 7. 安全与隔离检查清单

- [ ] 后端/Agent 只监听 `127.0.0.1` 或内网地址
- [ ] 云安全组 / 防火墙限制 8080、19090 到管理网段
- [ ] 没有把任何端口映射到公网
- [ ] `deploy/.env`、`/etc/linux-ops/backend.env` 权限 600
- [ ] `linuxops` 为普通用户，无多余 sudo 权限
- [ ] MySQL/MinIO 端口只绑定 `127.0.0.1`
- [ ] 已知悉：审计 operator 恒为 `anonymous`，无法识别具体人员
- [ ] 已知悉：本阶段无 TLS，链路可能被窃听/篡改

## 8. 日常运维

### 8.1 查看状态与日志

```bash
systemctl status linux-ops-backend linux-ops-agent
journalctl -u linux-ops-backend -n 200 --no-pager
journalctl -u linux-ops-agent -n 200 --no-pager
docker compose -f deploy/docker-compose.yml ps
docker compose -f deploy/docker-compose.yml logs -f mysql
```

### 8.2 备份

元数据（MySQL）：

```bash
mkdir -p /var/backups/linux-ops
docker exec linux-ops-mysql sh -c \
  'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction --routines --triggers linux_ops' \
  > /var/backups/linux-ops/mysql-$(date +%F-%H%M).sql
```

任务日志：

```bash
tar czf /var/backups/linux-ops/logs-$(date +%F-%H%M).tar.gz \
  -C /var/lib/linux-ops logs
```

建议把备份复制到另一台机器，并定期做恢复演练。

### 8.3 恢复

```bash
systemctl stop linux-ops-backend
docker exec -i linux-ops-mysql sh -c \
  'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" linux_ops' < backup.sql
tar xzf logs-YYYY-MM-DD-HHMM.tar.gz -C /var/lib/linux-ops
systemctl start linux-ops-backend
```

恢复后后端会自动迁移缺失的表结构（若 `auto_migrate=true`）。
历史任务日志能否读取取决于备份时的日志目录。

### 8.4 升级

```bash
# 1) 备份
# 2) 更新代码
bash scripts/build.sh
sudo systemctl stop linux-ops-backend linux-ops-agent
sudo install -m 0755 bin/backend /opt/linux-ops-platform/bin/backend
sudo install -m 0755 bin/agent   /opt/linux-ops-platform/bin/agent
sudo rm -rf /opt/linux-ops-platform/frontend/*
sudo cp -r frontend/dist/. /opt/linux-ops-platform/frontend/
sudo systemctl start linux-ops-agent linux-ops-backend
```

升级前先备份；`AutoMigrate` 只做增量变更，不会自动回滚。

### 8.5 日志清理

当前版本不自动清理 `/var/lib/linux-ops/logs`。建议：

- 监控磁盘使用率
- 按保留期删除旧任务目录（目录名是父任务 UUID）
- 删除前确认任务历史中不再需要对应日志

示例（保留 30 天，按修改时间）：

```bash
find /var/lib/linux-ops/logs -mindepth 2 -maxdepth 2 -type d -mtime +30 -print
# 确认无误后再加 -exec rm -rf {} +
```

## 9. 反向代理（可选）

如果需要在管理网段用统一入口访问（仍然**没有认证**），可用 Nginx。
完整示例见 [deploy/nginx.conf.example](../deploy/nginx.conf.example)，关键是
WebSocket 升级头必须正确转发：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
}

location /api/v1/ws/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 3600s;
}
```

不要在无认证的情况下套公网域名或开放 443。

### 9.1 自定义前缀部署（前端与后端前缀可以不同）

两个变量互相独立：

| 变量 | 含义 | 示例 |
|---|---|---|
| `VITE_BASE_PATH` | 前端静态文件（index.html、`/assets`）挂载路径 | `/ops-wxgzt/` |
| `VITE_API_BASE` | 浏览器访问后端 `/api/v1` 的**完整基址**，REST、下载链接、WebSocket 共用 | `/ops-server/api/v1` |

构建时传入（也可写入 `frontend/.env.local`，模板见
[frontend/.env.example](../frontend/.env.example)）：

```bash
cd frontend
VITE_BASE_PATH=/ops-wxgzt/ VITE_API_BASE=/ops-server/api/v1 npm run build
# 产物目录自动带上前缀：
#   dist/ops-wxgzt/index.html
#   dist/ops-wxgzt/assets/...
# index.html 引用 /ops-wxgzt/assets/...
# 打包后的请求地址为 /ops-server/api/v1/...
```

把整个 `dist/` 拷到部署目录（例如 `/opt/linux-ops-platform/frontend/`）后，
目录结构就是 `frontend/ops-wxgzt/index.html`，与 URL `/ops-wxgzt/` 一一对应，
不需要再手工搬文件。不设 `VITE_BASE_PATH` 时产物仍是平铺的 `dist/index.html`。

> **Windows Git Bash / MSYS 用户注意**：MSYS 会把形如 `/ops-wxgzt/` 的环境
> 变量自动转换成 `C:/Program Files/Git/ops-wxgzt/`，导致构建产物路径错误
> （白屏）。请去掉前导斜杠（`VITE_BASE_PATH=ops-wxgzt
> VITE_API_BASE=ops-server/api/v1`），或写进 `frontend/.env.local` 再构建。
> 两个变量都会自动补齐前导 `/`，`VITE_BASE_PATH` 还会补齐结尾 `/`。

Nginx 侧使用 [deploy/nginx-prefix.conf.example](../deploy/nginx-prefix.conf.example)：

- `/ops-wxgzt/` → 静态文件（`alias` 到 `frontend/ops-wxgzt/`，即 dist 里带前缀的那层目录）
- `/ops-server/` → `proxy_pass http://127.0.0.1:8080/;`（剥掉 `/ops-server`，
  后端路由仍是 `/api/v1`，无需改后端配置）
- `/ops-server/api/v1/ws/` → 单独转发 `Upgrade`/`Connection`

访问地址为 `http://<IP>/ops-wxgzt/`，页面内路由为 hash 模式
（`/ops-wxgzt/#/servers`），不需要 history 回退。

注意事项：

- **构建值与 Nginx 前缀必须完全一致**，只配一边会出现白屏或 404。
- `VITE_API_BASE` 要填“浏览器能访问到后端 `/api/v1` 的完整路径”。如果你的
  网关把 `/ops-server/` 直接映射到后端根路径，就填 `/ops-server/api/v1`；
  如果映射到 `/api/v1`，则填 `/ops-server`。
- 使用前缀时由 Nginx 提供静态文件；后端内置的静态托管仍假设根路径 `/`，
  两者不要混用。
- 不设这两个变量时行为与以前完全一致（`/` + `/api/v1`）。
- 开发服务器同样支持：`VITE_BASE_PATH=ops-wxgzt VITE_API_BASE=ops-server/api/v1
  npm run dev`，访问 `http://127.0.0.1:5173/ops-wxgzt/`，Vite 会把
  `/ops-server/api/v1` 代理并还原成 `/api/v1` 转发。

## 10. 故障排查

| 现象 | 排查 |
|---|---|
| 后端启动失败 | `journalctl -u linux-ops-backend`；确认 MySQL 已就绪、密码无特殊字符（建议 hex） |
| `ping mysql` 失败 | `docker compose ps`；端口是否 127.0.0.1:3306；`deploy/.env` 与 `backend.env` 密码是否一致 |
| 服务器一直 `offline` | `ss -lntp \| grep 19090`；`curl 127.0.0.1:19090/api/v1/info`；Agent 的 `listen_addr`；防火墙 |
| 命令失败但 Agent 在线 | 查看子任务 `error`；`work_dir` 是否存在且可写；`ProtectHome`/`PrivateTmp`；是否缺少 sudo |
| 任务一直 `PENDING` | 并发槽已满；查看是否有卡住的 `RUNNING` 任务；`dispatch_concurrency` |
| 状态 `UNKNOWN` | Agent 重启/不可达导致状态丢失。这是保守设计，不会重跑；可人工重试 |
| 前端 404 或空白 | `FRONTEND_DIR` 是否正确；`frontend/dist/index.html` 是否存在；重新 `npm run build` |
| WebSocket 连不上 | 代理是否转发 `Upgrade`；`ws: true`（Vite dev）；防火墙；连接数上限 |
| 日志为空 | `LOG_DIR` 权限（`linuxops` 可写）；任务是否真的产生输出；是否超过输出上限被截断 |
| 下载 404 | 该目标没有落盘日志（例如尚未开始或已清理） |
| 时间显示不对 | 数据库/后端使用 UTC，前端按浏览器时区显示；检查服务器时间同步 |

## 11. 卸载

```bash
sudo bash scripts/uninstall.sh              # 停止并移除服务，保留数据
sudo bash scripts/uninstall.sh --purge-data # 同时删除 MySQL 卷与 /var/lib/linux-ops
```

二进制仍保留在 `/opt/linux-ops-platform`，确认后可手工删除。
