# wxgzt-ops 生产部署手册

本文面向把平台部署到**已有 MySQL 8 与 MinIO 的生产/内网环境**的运维人员。
与 `docs/deployment.md`（单机 Docker 演示安装）不同，本文不下载 Go/Node、不启动
Docker Compose，只使用现成的数据库、对象存储和 nginx。

部署拓扑（示例地址，按实际替换）：

```
管理员浏览器 ──HTTP/HTTPS──> nginx :80/:443
                               ├── /ops-wxgzt/   静态前端（dist）
                               └── /ops-server/  反向代理 -> backend 127.0.0.1:8089
                                                       │
                                        MySQL 10.0.0.20:3306
                                                       │
受管服务器 A 10.0.0.31 ── 19090 ──> 只接受 backend 源 IP 10.0.0.5
受管服务器 B 10.0.0.32 ── 19090 ──> （allowed_ips 白名单）
```

> **安全边界（先读）**
> - Agent 的 19090 **没有请求认证**，只能靠 `allowed_ips` 源地址白名单 +
>   防火墙/安全组限制访问。白名单必须写具体 IP/CIDR，`0.0.0.0`、`::`、
>   `0.0.0.0/0` 会被 Agent 和安装脚本直接拒绝。
> - 后端提供用户名/密码、JWT 与 API 令牌，但默认仍是明文 HTTP。跨网访问请
>   在 nginx 终止 TLS，或只放在隔离管理网内。
> - 二进制可以加固（Agent 用 garble 混淆，两者都去符号），但**不能被真正加密后
>   仍直接执行**。不要用二进制保密替代网络隔离和权限控制。

---

## 1. 固定目录与命名

所有生产环境统一使用 `wxgzt-ops`：

| 内容 | 路径 / 名称 |
|---|---|
| 程序目录 | `/opt/wxgzt-ops` |
| 后端二进制 | `/opt/wxgzt-ops/bin/backend` |
| Agent 二进制 | `/opt/wxgzt-ops/bin/wxgzt-ops` |
| 前端静态文件 | `/opt/wxgzt-ops/frontend/ops-wxgzt/` |
| 配置文件 | `/etc/wxgzt-ops/backend.yaml`、`/opt/wxgzt-ops/agent.yaml` |
| 环境变量文件 | `/etc/wxgzt-ops/backend.env`（600） |
| 日志目录 | `/var/lib/wxgzt-ops/logs` |
| 运行用户 | `root`（两个服务均以 root 运行，不创建系统用户） |
| systemd 服务 | `wxgzt-ops-backend`、`wxgzt-ops` |
| MinIO 桶 | `wxgzt-ops` |
| 制品命名 | `wxgzt-ops-agent-<版本>-linux-<arch>.tar.gz` |

Agent 安装脚本会自动停用旧版 `linux-ops-agent` 服务并释放 19090，便于从旧部署
迁移；旧配置不会自动删除。

---

## 2. 生产环境准备

### 2.1 MySQL

在 MySQL 上创建库和最小权限账号（账号来源地址填**后端主机 IP**）：

```sql
CREATE DATABASE IF NOT EXISTS wxgzt_ops
  CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE USER 'wxgzt'@'10.0.0.5' IDENTIFIED BY '替换成强密码';
GRANT ALL PRIVILEGES ON wxgzt_ops.* TO 'wxgzt'@'10.0.0.5';
FLUSH PRIVILEGES;
```

后端启动时会自动建表（`mysql.auto_migrate: true`）。如需手工建表，可先执行
`database/schema.sql`，再把 `auto_migrate` 设为 `false`。
`schema.sql` 只包含表结构，不含任何账号数据；内置 `admin` 由后端首次启动时创建
（依赖 `mysql.seed: true`，初始口令取 `ACCESS_PASSWORD`，未设置则为 `changeme`）。
`schema.sql` 默认库名是 `linux_ops`，生产库名不同时先替换（或手工改开头两行）：

```bash
sed 's/linux_ops/wxgzt_ops/g' database/schema.sql |
  mysql -h 10.0.0.20 -P 3306 -u wxgzt -p
```

连通性自测（在后端主机执行）：

```bash
mysql -h 10.0.0.20 -P 3306 -u wxgzt -p -e 'SELECT 1' wxgzt_ops
```

### 2.2 MinIO

创建桶并确认安装脚本、Agent 自更新可以匿名读取：

```bash
mc alias set wxminio https://minio.internal:9000 <ACCESS_KEY> <SECRET_KEY>
mc mb --ignore-existing wxminio/wxgzt-ops
mc anonymous set download wxminio/wxgzt-ops
mc ls wxminio/wxgzt-ops
```

> `bin/minio_upload.py` 会写入一条同时包含 `s3:GetObject` 与 `s3:ListBucket`
> 的只读策略，Agent 的版本发现和安装脚本的版本扫描都依赖 ListBucket。
> 桶内只有安装包和脚本，没有业务数据；不要把写权限给受管服务器。

### 2.3 nginx 与端口

| 端口 | 绑定 | 暴露范围 |
|---|---|---|
| 80/443 | nginx | 仅管理网段 |
| 8089 | backend | 仅 `127.0.0.1`（由 nginx 反代） |
| 19090 | Agent | 仅 `allowed_ips` 中的后端 IP |
| 3306 / 9000 | MySQL / MinIO | 仅后端主机 / 构建机 |

若使用云安全组，同样只放行上述来源。

---

## 3. 构建发布包（构建机）

构建机可以是当前 Windows/macOS/Linux 开发机。要求 Go 1.22+、Node 18+。

### 3.1 一次性准备

```bash
go install mvdan.cc/garble@v0.12.1
# 确认 $GOPATH/bin 在 PATH 中
garble version
```

`scripts/build-release.sh` 只对 **Agent** 使用 garble 混淆，两个二进制都加
`-trimpath`、`-s -w` 去除符号与路径信息。**后端不参与 garble**：GORM 通过反射
从 Go 类型名和字段名推导表名/列名，混淆会把这些标识符改成随机名，后端就会去查
`rd_yvyos` 之类的表，与 `database/schema.sql` 完全不匹配。没有 garble 时脚本会
拒绝构建（可用 `--no-obfuscate` 仅用于调试，此时 Agent 也不混淆）。

### 3.2 生成后端、Agent 与校验和

```bash
cd /path/to/linux-ops-platform
scripts/build-release.sh                     # linux amd64 + arm64
scripts/build-release.sh --archs amd64       # 只构建 amd64
scripts/build-release.sh --with-frontend     # 同时构建前端
```

输出分为两部分（下面以 `0.2.0` 为例，实际以 `agent/main.go` 的版本号为准）：

```
bin/release/                             ← 发布到 MinIO 的 Agent 制品桶
  wxgzt-ops-agent-0.2.0-linux-amd64.tar.gz   Agent 安装包（二进制/配置/unit/安装脚本）
  wxgzt-ops-agent-0.2.0-linux-arm64.tar.gz
  SHA256SUMS                                 本地构建记录；上传时由
                                             bin/minio_upload.py 写进安装脚本，
                                             不需要上传到桶里

bin/release/server/                      ← 管理端部署文件，不要上传到 Agent 桶
  wxgzt-ops-backend-0.2.0-linux-amd64       后端二进制
  wxgzt-ops-backend-0.2.0-linux-arm64
  wxgzt-ops-server-0.2.0-linux-amd64.tar.gz 后端 + 前端 + configs +
                                            systemd/nginx + database/schema.sql
  wxgzt-ops-server-0.2.0-linux-arm64.tar.gz
  wxgzt-ops-frontend-0.2.0.tar.gz           仅前端产物（--with-frontend）
  SHA256SUMS                                管理端制品校验和
```

Agent 桶里只需要 `wxgzt-ops-agent-<ver>-linux-<arch>.tar.gz` 和
`agent-install.sh` 两类对象；`server/` 下的任何文件都不要上传到 Agent 桶。

`agent-install.sh` 由 `bin/minio_upload.py` 从 `scripts/agent-install.sh` 上传，
上传时会把实际桶地址和本次构建的 `SHA256SUMS` 一起写入副本。server 包不包含
它，因为管理端不执行安装器。

Agent 包内也带一份 `agent-install.sh` 副本，便于解包后在主机上离线重装；它没有
内置校验和（否则会改变自身所在包的摘要），桶里也没有 `SHA256SUMS` 时需显式设置
`AGENT_ALLOW_NO_CHECKSUM=1` 才能安装。首次自举应从桶根目录执行脚本。

Agent 包内结构固定为
`wxgzt-ops/{wxgzt-ops,agent.yaml,wxgzt-ops.service,agent-install.sh,README.md}`。
加固说明：

- Agent 使用 garble 混淆，后端只用 `-trimpath -s -w`：GORM 依赖 Go 标识符名
  推导表/列名，后端混淆会直接导致查错表。去符号能增加反编译成本，但 Go 程序
  运行时必须保留可执行结构，无法做到“加密后才能运行”。
- 前端是浏览器要执行的 JavaScript，构建已压缩并禁用 source map；**不要把
  口令、密钥写进前端**。
- 可选的 `--upx` 只做压缩壳，可能触发杀软误报，默认不启用。

### 3.3 构建前端

`frontend/.env.local` 已固定生产前缀：

```
VITE_BASE_PATH=/ops-wxgzt/
VITE_API_BASE=/ops-server/api/v1
```

```bash
cd frontend
npm ci
npm run build
# 产物：dist/ops-wxgzt/index.html + dist/ops-wxgzt/assets/
```

也可以直接用 `scripts/build-release.sh --with-frontend`：脚本会显式传入
`VITE_BASE_PATH=/ops-wxgzt/`、`VITE_API_BASE=/ops-server/api/v1`（可用同名
环境变量覆盖），并把产物打进 `bin/release/server/` 的部署包。

### 3.4 上传制品到 MinIO

推荐用仓库自带工具（生成安装包校验和、写入并上传 `agent-install.sh`、设置只读策略）：

```bash
export MINIO_ENDPOINT=https://minio.internal:9000
export MINIO_ACCESS_KEY=<写权限账号>
export MINIO_SECRET_KEY=<写权限密钥>
export MINIO_BUCKET=wxgzt-ops

python3 bin/minio_upload.py --file wxgzt-ops-agent-0.2.0-linux-amd64.tar.gz
python3 bin/minio_upload.py --file wxgzt-ops-agent-0.2.0-linux-arm64.tar.gz \
  --notes "支持多架构与来源白名单"
```

脚本只上传安装包和渲染后的 `agent-install.sh`：当前的
`MINIO_ENDPOINT/MINIO_BUCKET` 和 `SHA256SUMS` 内容都会写进上传副本，因此桶里
不需要额外的校验和文件，受管主机也可以不带参数直接执行。

也可以手工 `mc cp`：

```bash
mc cp bin/release/wxgzt-ops-agent-0.2.0-linux-amd64.tar.gz \
      bin/release/wxgzt-ops-agent-0.2.0-linux-arm64.tar.gz \
      scripts/agent-install.sh wxminio/wxgzt-ops/
```

手工上传的脚本没有写入桶地址和校验和，地址会回落到
`https://minio.internal:9000/wxgzt-ops`（其他地址需传 `--base-url`）；由于桶里
也没有 `SHA256SUMS`，安装会被拒绝，需配合 `AGENT_ALLOW_NO_CHECKSUM=1` 或改用
`bin/minio_upload.py`。

上传后验证（模拟 Agent 的匿名访问）：

```bash
curl -fsSL "https://minio.internal:9000/wxgzt-ops/?list-type=2&prefix=wxgzt-ops-agent-"
curl -fsSL "https://minio.internal:9000/wxgzt-ops/agent-install.sh" | head -n 3
```

建议 MinIO 走 HTTPS：安装脚本和 Agent 自更新都通过这个地址下载二进制，
明文 HTTP 只能用于完全隔离的内网。

---

## 4. 部署后端

### 4.1 安装文件

在后端主机（示例 10.0.0.5）执行：

```bash
# 服务以 root 运行，不创建任何系统用户
install -d -m 0755 /opt/wxgzt-ops/bin /opt/wxgzt-ops/docs
install -d -m 0750 /var/lib/wxgzt-ops/logs
install -d -m 0755 /etc/wxgzt-ops
```

从构建机上传：

```bash
scp bin/release/server/wxgzt-ops-backend-0.2.0-linux-amd64 \
    root@10.0.0.5:/opt/wxgzt-ops/bin/backend
scp configs/backend.yaml root@10.0.0.5:/etc/wxgzt-ops/backend.yaml
scp deploy/systemd/wxgzt-ops-backend.service \
    root@10.0.0.5:/etc/systemd/system/wxgzt-ops-backend.service
```

设置权限：

```bash
chown root:root /opt/wxgzt-ops/bin/backend
chmod 0755 /opt/wxgzt-ops/bin/backend
chmod 0644 /etc/wxgzt-ops/backend.yaml /etc/systemd/system/wxgzt-ops-backend.service
```

> 两个服务都以 root 运行，`/opt/wxgzt-ops/bin/` 无需为专用用户授权；Agent 的自更新
> 直接由 root 写回同一目录。

### 4.2 环境变量文件（含 MySQL / MinIO / JWT）

`/etc/wxgzt-ops/backend.env`（`chmod 600`）：

```bash
# ---- MySQL ----
DB_HOST=10.0.0.20
DB_PORT=3306
DB_USER=wxgzt
DB_NAME=wxgzt_ops
DB_PASSWORD=替换成强密码
# 也可用完整 DSN 覆盖上面的字段：
# DB_DSN=wxgzt:密码@tcp(10.0.0.20:3306)/wxgzt_ops?charset=utf8mb4&parseTime=True&loc=UTC

# ---- 监听与前端 ----
LISTEN_ADDR=127.0.0.1:8089
ALLOW_ORIGIN=http://10.0.0.10
LOG_DIR=/var/lib/wxgzt-ops/logs
# 前端由 nginx 提供；若要后端直接托管，填 /opt/wxgzt-ops/frontend
FRONTEND_DIR=

# ---- 登录 / 令牌 ----
# 只在数据库还没有 admin 时生效；留空会创建 admin/changeme，生产务必显式设置
ACCESS_PASSWORD=替换成一次性强口令
SESSION_HOURS=12
JWT_SECRET=替换成 openssl rand -hex 32 的输出
JWT_MINUTES=120

# ---- MinIO（与 bin/minio_upload.py 共用；后端当前不直接读取对象）----
MINIO_ENDPOINT=https://minio.internal:9000
MINIO_ACCESS_KEY=<只读或发布账号>
MINIO_SECRET_KEY=<密钥>
MINIO_BUCKET=wxgzt-ops
MINIO_UPDATE_BASE_URL=https://minio.internal:9000/wxgzt-ops
```

说明：

- 当前版本的后端**不直连 MinIO**：安装包由 MinIO 直接提供给安装脚本和 Agent，
  Agent 自更新使用它自己的 `update_base_url`。`MINIO_*` 变量与发布工具保持
  同一套配置，便于后续版本复用，也避免在两处维护凭据。
- `JWT_SECRET` 留空时后端会生成并存入 `app_settings`；生产建议显式设置并妥善
  备份。更换该值会让所有未过期 JWT 立即失效。
- `ACCESS_PASSWORD` 只在首次建库时写入 `admin`。首次登录后立即在右上角修改
  密码；之后该变量不再覆盖数据库密码。
- `ALLOW_ORIGIN` 填浏览器访问的完整源（协议+IP/域名+端口）。前后端同源反代
  时 CORS 实际不生效，填明确值即可，不要用 `*` 配 Cookie。

```bash
chown root:root /etc/wxgzt-ops/backend.env
chmod 600 /etc/wxgzt-ops/backend.env
```

### 4.3 启动与自检

```bash
systemctl daemon-reload
systemctl enable --now wxgzt-ops-backend
systemctl status wxgzt-ops-backend --no-pager
journalctl -u wxgzt-ops-backend -n 50 --no-pager

curl -fsS http://127.0.0.1:8089/api/v1/health
```

浏览器先经 nginx 访问（见下一节），用 `admin` 和 `ACCESS_PASSWORD` 登录后：

1. 右上角修改管理员密码；
2. 在「用户管理」创建子用户；
3. 在「令牌」按需创建 API 令牌（明文只显示一次）。

---

## 5. 部署前端与 nginx

### 5.1 放置静态文件

```bash
install -d -m 0755 /opt/wxgzt-ops/frontend
rsync -a --delete frontend/dist/ops-wxgzt/ \
  root@10.0.0.10:/opt/wxgzt-ops/frontend/ops-wxgzt/
```

最终应是 `/opt/wxgzt-ops/frontend/ops-wxgzt/index.html` 与 `assets/`。

### 5.2 nginx 配置

把下面内容保存为 `/etc/nginx/conf.d/wxgzt-ops.conf`（与
`deploy/nginx-prefix.conf.example` 一致，按实际地址修改）：

```nginx
server {
    listen 10.0.0.10:80;
    server_name _;

    # 管理网段白名单（与安全组二选一或同时使用）
    allow 10.0.0.0/24;
    deny all;

    client_max_body_size 64m;

    location = /ops-wxgzt {
        return 301 /ops-wxgzt/;
    }

    location /ops-wxgzt/assets/ {
        alias /opt/wxgzt-ops/frontend/ops-wxgzt/assets/;
        expires 30d;
        add_header Cache-Control "public, immutable";
        access_log off;
    }

    location = /ops-wxgzt/index.html {
        alias /opt/wxgzt-ops/frontend/ops-wxgzt/index.html;
        add_header Cache-Control "no-cache";
    }

    location /ops-wxgzt/ {
        alias /opt/wxgzt-ops/frontend/ops-wxgzt/;
        index index.html;
    }

    # WebSocket：必须保留 Upgrade/Connection，否则实时输出与终端断开
    location /ops-server/api/v1/ws/ {
        proxy_pass http://127.0.0.1:8089/api/v1/ws/;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        proxy_buffering off;
    }

    # REST：/ops-server/api/v1/... -> 127.0.0.1:8089/api/v1/...
    location /ops-server/ {
        proxy_pass http://127.0.0.1:8089/;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

```bash
nginx -t && systemctl reload nginx
```

访问 `http://10.0.0.10/ops-wxgzt/`。如果前面有 TLS 终止，把页面里的
`ALLOW_ORIGIN` 改成对应的 `https://...` 源。

排查要点：

- 页面 404/白屏：确认 `dist/ops-wxgzt/` 整目录被复制，而不是只复制了 `dist/`。
- 接口 404：`/ops-server/api/v1/health` 应返回 JSON；检查 `proxy_pass` 末尾的 `/`。
- WebSocket 失败：检查 `Upgrade`/`Connection` 两行，以及浏览器是否被 HTTPS 混合内容拦截。

---

## 6. Agent 打包、安装与白名单

### 6.1 安装包与校验

Agent 包由 `scripts/build-release.sh` 生成，桶内只需要两类对象：

- `wxgzt-ops-agent-<x.y.z>-linux-<arch>.tar.gz`；
- `agent-install.sh`。

`bin/minio_upload.py` 上传时会把实际桶地址和本次构建的 `SHA256SUMS` 写进
脚本副本，安装时据此校验下载的安装包，因此桶里不再需要 `SHA256SUMS` 或
`manifest.json`。手工 `mc cp` 上传的脚本没有内置校验和，会回退到从桶里读取
`SHA256SUMS`/`manifest.json`；两者都没有时安装会被拒绝，除非显式设置
`AGENT_ALLOW_NO_CHECKSUM=1`。

> 校验和固化在脚本里，替换桶内安装包后必须重新运行 `bin/minio_upload.py`，
> 让 `agent-install.sh` 里的 SHA256 同步更新。

安装包结构固定为
`wxgzt-ops/{wxgzt-ops,agent.yaml,wxgzt-ops.service,agent-install.sh,README.md}`。

### 6.2 安装命令（零参数）

在**受管服务器**上执行（root）：

```bash
curl -fsSL https://minio.internal:9000/wxgzt-ops/agent-install.sh | sudo bash
```

不需要任何其他参数。安装包内自带 `agent.yaml`，脚本会：

1. 按架构选择版本，用脚本内置 SHA256 校验安装包；
2. 解压并安装二进制到 `/opt/wxgzt-ops/bin/wxgzt-ops`；
3. 直接把包内的 `agent.yaml` 放到 `/opt/wxgzt-ops/agent.yaml` 作为运行配置
   （已有同名文件时保留；`--force` 才用包内配置覆盖；旧的
   `/etc/wxgzt-ops/agent.yaml` 会自动迁移过来）；
4. 包内 `update_base_url` 若是 `127.0.0.1` 占位值，改成这次实际使用的制品
   桶地址，其余字段保持包内内容不变；
5. 安装并启动 systemd 服务 `wxgzt-ops`，停用旧版 `linux-ops-agent`；
6. 用配置中的监听地址探测健康接口，`200` 或 `403` 都表示进程已监听
   （`403` = 服务正常但探测来源不在白名单）。

因此打包前要按环境改好 `configs/agent.yaml`，通常只需要改 `allowed_ips`
（后端主机 IP）。`listen_addr` 默认是 `auto`：Agent 启动时自动选择本机主用
内网 IPv4（优先 RFC1918，跳过 `docker0`/veth 等虚拟网卡），所以同一份配置
可以直接发到所有主机，不必逐台修改。多网卡且后端走非主用网段时，再给该主机
显式指定 `listen_addr`。也可以在装好后编辑 `/opt/wxgzt-ops/agent.yaml`，
再执行 `systemctl restart wxgzt-ops`。Agent 不接受空白名单或通配地址。

> HTTPS 制品桶：脚本内部下载默认使用 `curl -k` 跳过证书校验（适配内网自签名
> MinIO）；要强制校验证书，设置 `AGENT_TLS_VERIFY=1`。外层这行 `curl` 不由脚本
> 控制，自签名证书下请写成 `curl -k -fsSL ... | sudo bash`。`-k` 会削弱传输层
> 认证，生产更稳妥的做法是给 MinIO 配置受信任证书；包完整性仍由安装脚本内置的
> SHA256 校验。

可选参数（正常安装不需要）：

| 参数 | 含义 |
|---|---|
| `--config` | 用指定的完整 `agent.yaml` 覆盖 `/opt/wxgzt-ops/agent.yaml` |
| `--listen-addr` | 生成新配置时写死的本机内网 IP；默认使用包内配置（`auto`，由 Agent 启动时探测）；**不接受 0.0.0.0/::** |
| `--allow-ip` | 生成新配置时使用的来源 IP/CIDR，可重复或逗号分隔；`0.0.0.0/0`、`::/0`、`*` 会被拒绝 |
| `--base-url` | 制品桶地址（可选），默认使用脚本内写入的地址 |
| `--version` | 指定版本；默认选桶内该架构最高版本 |
| `--port` | 仅生成新配置时使用，默认 19090 |
| `--force` | 用包内 `agent.yaml` 覆盖已有的 `/opt/wxgzt-ops/agent.yaml` |

环境变量等价形式：`AGENT_LISTEN_ADDR`、`AGENT_ALLOW_IPS`（逗号分隔）、
`AGENT_BASE_URL`、`AGENT_CONFIG`、`AGENT_VERSION`、`AGENT_FORCE`。

注意：

- `--base-url` 可选；不传时使用脚本内写入的桶地址（手工 `mc cp` 的副本回落到
  `https://minio.internal:9000/wxgzt-ops`）。
- 包内或指定的配置必须包含非空 `allowed_ips`，且不能是通配地址。Agent 启动时
  会再次校验；配置非法会导致服务启动失败，安装脚本会打印 journal 并退出。
- 健康探测使用配置中的 `listen_addr:listen_port`；`listen_addr` 为 `auto`
  时用与本机相同的路由规则探测实际地址（探测不到时回退 `127.0.0.1:19090`）。
  安装完成输出的就是 Agent 实际监听地址，平台里登记的主机 IP 应与之一致。
- 已有配置存在时，传入的 `--allow-ip` / `--listen-addr` 会被忽略并给出提示；
  要用命令行参数重新生成，请加 `--force` 或 `--config`。

**多后端 / 高可用**：把每个后端主机（或经 NAT 后的真实源地址）都写进打包前
`configs/agent.yaml` 的 `allowed_ips`。如果后端在容器或负载均衡后面，白名单要
写实际发起连接的源 IP，不是浏览器 IP。

安装过程只输出几行结果；Agent 运行期**不写日志文件**，只把错误写入
journald，并启用速率限制。

### 6.3 验证白名单

在后端主机（`10.0.0.5`）执行：

```bash
curl -sS http://10.0.0.31:19090/api/v1/health
curl -sS http://10.0.0.31:19090/api/v1/info
```

在未授权主机执行，应得到 `403 forbidden: source ip is not in allowed_ips`
或被防火墙丢弃：

```bash
curl -sS --max-time 3 http://10.0.0.31:19090/api/v1/health
```

查看拒绝日志（每个来源 IP 每分钟最多一行）：

```bash
journalctl -u wxgzt-ops -n 20 --no-pager
```

### 6.4 防火墙（纵深防御）

Agent 自身已经按来源 IP 拒绝，但仍建议在主机防火墙只放行后端地址。
firewalld 示例：

```bash
for ip in 10.0.0.5 10.0.0.6; do
  firewall-cmd --permanent --add-rich-rule="rule family=ipv4 \
    source address=${ip} port port=19090 protocol=tcp accept"
done
firewall-cmd --reload
```

ufw 示例：

```bash
ufw allow from 10.0.0.5 to any port 19090 proto tcp
ufw allow from 10.0.0.6 to any port 19090 proto tcp
```

云环境同步收紧安全组。**不要**使用 `0.0.0.0/0`。

### 6.5 在平台中添加主机

「服务器管理」→ 新增：IP 填 Agent 的 `listen-addr`，端口 19090。
后端通过内网调用 `http://<IP>:19090`，不经过 nginx。

---

## 7. 升级与回滚

### 7.1 Agent 自更新

1. 构建新版本（例如 `0.2.1`），上传 tarball 到 MinIO；
2. 平台「服务器管理」→ 检查更新 / 批量更新；
3. Agent 下载、校验 SHA256、替换自身二进制并 re-exec。

Agent 从桶内 `wxgzt-ops-agent-*` 文件名发现版本，因此**只需上传正确命名的
tarball**。当前发布流程把校验和写进安装脚本，桶里不再保留 `SHA256SUMS`，所以
自更新只校验下载完整性而无法比对发布校验和；包内配置因此使用
`update_require_checksum: false`。若要恢复自更新的 SHA256 校验，需要在桶里额外
保留 `SHA256SUMS` 并把该项设为 `true`。

### 7.2 Agent 重装/改配置

```bash
# 重装最新版，并保留现有 /opt/wxgzt-ops/agent.yaml
curl -fsSL https://minio.internal:9000/wxgzt-ops/agent-install.sh | sudo bash

# 用安装包内的 agent.yaml 覆盖现有配置
curl -fsSL https://minio.internal:9000/wxgzt-ops/agent-install.sh | sudo bash -s -- --force
```

不加 `--force` 时保留现有 `/opt/wxgzt-ops/agent.yaml`；需要临时改白名单可直接
编辑该文件后 `systemctl restart wxgzt-ops`。

### 7.3 后端升级

```bash
systemctl stop wxgzt-ops-backend
install -m 0755 -o root -g root \
  bin/release/server/wxgzt-ops-backend-0.2.1-linux-amd64 /opt/wxgzt-ops/bin/backend
systemctl start wxgzt-ops-backend
curl -fsS http://127.0.0.1:8089/api/v1/health
```

启动时自动执行数据库迁移。回滚时换回旧二进制并重启即可；数据库结构变更前
请先备份。

### 7.4 前端升级

重新 `npm run build`，用 `rsync -a --delete` 覆盖
`/opt/wxgzt-ops/frontend/ops-wxgzt/`，nginx 无需重启（`index.html` 已设置
`no-cache`，带哈希的 assets 可长期缓存）。

---

## 8. 日志、存储与审计

- Agent：`StandardOutput=null`，仅错误进 journald，`LogRateLimitIntervalSec=30s`，
  不创建日志文件；`terminal.history_mode: memory` 默认不写目标用户
  `~/.bash_history`。
- 后端：日志在 `LOG_DIR`（默认 `/var/lib/wxgzt-ops/logs`），按需轮转。
- journald 可设上限，例如 `/etc/systemd/journald.conf`：

  ```ini
  SystemMaxUse=200M
  ```

  修改后 `systemctl restart systemd-journald`。
- **不要删除**系统 auditd/journald 的审计记录来“清理痕迹”；平台自身的登录、
  改密、令牌、命令下发审计在数据库中。按合规要求做归档和保留，而不是删除。

---

## 9. 安全清单（上线前逐项确认）

- [ ] `admin` 默认口令已修改；子用户按最小权限创建。
- [ ] `ACCESS_PASSWORD`、`DB_PASSWORD`、`JWT_SECRET` 均为随机强值，`backend.env` 权限 600。
- [ ] 后端只监听 `127.0.0.1:8089`，由 nginx/内网反代访问；未直接暴露 8089。
- [ ] nginx 仅监听管理地址，并用 `allow/deny` 或安全组限制来源。
- [ ] Agent `allowed_ips` 只包含后端主机 IP/CIDR，**没有** `0.0.0.0`、`::`、`0.0.0.0/0`。
- [ ] 19090 在主机防火墙/安全组中只对后端 IP 开放。
- [ ] 已知悉：后端与 Agent 均以 root 运行（未创建专用用户），平台账号或令牌泄露等同受管主机 root 权限，必须严格隔离和审计。
- [ ] 安装包来自 HTTPS 或可信内网，安装脚本内置的 SHA256 校验通过（替换安装包后已重跑 `bin/minio_upload.py`）；MinIO 写权限只在构建机。
- [ ] MySQL 账号只允许后端主机来源，仅授权业务库。
- [ ] Agent 使用 garble 构建、后端未混淆（避免 GORM 反射改名）；两者均去符号；前端无 source map、无硬编码密钥。
- [ ] 已规划 MySQL 备份与恢复演练；日志保留策略明确。
- [ ] 明确知晓：受管服务器上的 Agent 无请求认证，控制它的唯一手段是网络白名单。

---

## 10. 故障排查

| 现象 | 排查 |
|---|---|
| 后端启动失败 / 退出 | `journalctl -u wxgzt-ops-backend -n 50`；核对 `DB_*`、MySQL 授权来源、库是否存在 |
| 页面能开但接口 401 | 是否经 nginx 同源访问；`ALLOW_ORIGIN` 是否与浏览器地址一致；Cookie 是否被 HTTPS/代理丢弃 |
| 页面接口 404 | `/ops-server/api/v1/health` 是否命中；`proxy_pass` 末尾 `/`；前端是否按 `/ops-server/api/v1` 构建 |
| 静态资源 404 | nginx `alias` 是否指向 `/opt/wxgzt-ops/frontend/ops-wxgzt/`（dist 自带前缀目录） |
| Agent 服务起不来 | `journalctl -u wxgzt-ops`；常见原因：`allowed_ips` 为空/含通配、`listen_addr` 是 0.0.0.0、端口被旧 `linux-ops-agent` 占用（`ss -lntp | grep 19090`） |
| 后端访问 Agent 403 | 后端实际出口 IP 不在 `allowed_ips`（NAT/多网卡）；加 `--force` 重装并补上真实源 IP |
| 后端访问 Agent 超时 | 主机防火墙/安全组未放行；Agent 未监听在预期网卡 |
| Agent 检查更新失败 | 桶未开匿名 `ListBucket`/`GetObject`；文件名不符合 `wxgzt-ops-<x.y.z>-linux-<arch>.tar.gz`；MinIO 地址不可达 |
| TLS 下 WebSocket 失败 | nginx 需要转发 `Upgrade`/`Connection`；浏览器 `wss` 与页面同协议 |
| 安装脚本报“包结构异常” | 上传的是旧命名包；重新用 `scripts/build-release.sh` 生成 |

---

## 11. 相关文件

- `scripts/build-release.sh` — 生产构建（Agent 混淆、后端不混淆）、打包、校验和
- `scripts/agent-install.sh` — Agent 一键安装（零参数，使用包内 `agent.yaml`；可选 `--config` 或白名单参数）
- `bin/minio_upload.py` — 上传制品并设置只读策略
- `configs/backend.yaml` — 后端配置模板（环境变量优先）
- `configs/agent.yaml` — Agent 配置模板（安装脚本会重新生成）
- `deploy/systemd/wxgzt-ops-backend.service`、`deploy/systemd/wxgzt-ops.service`
- `deploy/nginx-prefix.conf.example` — 前端/后端前缀反代示例
