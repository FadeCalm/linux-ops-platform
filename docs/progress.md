# 验证进度（如实记录）

> 原则：**只记录真正执行过的验证**。Windows 开发机无法验证的东西，
> 一律放在「待 Linux 验证」中，不写成已完成。

最后更新：2026-11-06

## 环境

| 项 | 值 |
|---|---|
| 开发机 | Windows 11 Pro for Workstations，amd64 |
| Go | `go1.22.5 windows/amd64` |
| Node / npm | `v22.15.0` / `11.3.0` |
| 目标服务器 | Linux amd64，有外网，由用户自行部署 |
| MySQL / MinIO | 开发机没有；由 `deploy/docker-compose.yml` 在 Linux 提供 |

## 一、已在开发机验证

| # | 项目 | 命令 | 结果 |
|---|---|---|---|
| 1 | 后端格式 | `cd backend && gofmt -l .` | 通过（整理后无输出） |
| 2 | 后端编译 | `go build ./...` | 通过 |
| 3 | 后端静态检查 | `go vet ./...` | 通过 |
| 4 | 后端单元测试 | `go test -count=1 ./...` | 通过：`scheduler 0.184s`、`ws 0.163s` |
| 5 | Agent 格式 | `cd agent && gofmt -l .` | 通过（整理后无输出） |
| 6 | Agent 编译 | `go build ./...` | 通过 |
| 7 | Agent 静态检查 | `go vet ./...` | 通过 |
| 8 | Agent 单元测试 | `go test -count=1 ./...` | 通过：`exec 0.138s`（仅 `buffer_test.go`；Linux 用例被 build tag 排除） |
| 9 | 后端交叉编译 | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build .` | 通过，产物约 15 MB |
| 10 | Agent 交叉编译 | `GOOS=linux GOARCH=amd64/arm64 CGO_ENABLED=0 go build .` | 通过，产物约 7.9 MB / 7.7 MB |
| 11 | 前端安装 | `cd frontend && npm install` | 通过 |
| 12 | 前端构建 | `npm run build` | 通过：365 modules，`dist/index.html`、JS 1,032.62 kB、CSS 248.93 kB |
| 13 | Agent HTTP 运行冒烟 | 在本机启动 Agent，`curl /health /info /version` | 通过，返回 `os=windows`、`version=0.1.0`、`status=ok` |

### 单元测试覆盖内容

| 测试 | 覆盖 |
|---|---|
| `backend/internal/scheduler/state_test.go` | 父任务聚合：全成功、全取消、运行中、混合失败、空集合等 |
| `backend/internal/ws/hub_test.go` | seq 单调递增、按游标重放、环形缓冲边界、连接数上限 |
| `agent/internal/exec/buffer_test.go` | 输出截断、按偏移切片、`truncated` 标记 |
| `agent/internal/exec/engine_linux_test.go` | **仅 Linux**：echo、非零退出码、超时、取消、重复 task_id 幂等 |

### 已知未在开发机执行的部分

- Windows 上 Agent 硬编码 `/bin/bash`，**没有**尝试执行命令。
- 后端依赖 MySQL，开发机没有 MySQL，**没有**启动过后端进程。
- 前端只验证了打包，**没有**在浏览器中连接真实后端做交互验证。

## 二、待 Linux 服务器验证

以下项目必须在部署目标上执行，完成前不得视为已验证：

| # | 项目 | 验收方法 |
|---|---|---|
| 1 | MySQL 建库/账号/docker compose | `docker compose ps`，后端启动日志出现 auto-migration |
| 2 | 后端启动与迁移 | `systemctl status`，`curl /api/v1/health` |
| 3 | 心跳与在线状态 | 服务器列表显示 `local-agent` 为 `online`，版本号正确 |
| 4 | 单机真实执行 | `bash tests/smoke.sh` PASS |
| 5 | stdout/stderr 分流 | smoke 脚本分别断言 `smoke-ok` / `smoke-err` |
| 6 | WebSocket 实时输出 | 前端「命令执行」页边执行边看到输出 |
| 7 | 退出码与耗时 | 执行 `exit 7`，状态 `FAILED`，退出码 7 |
| 8 | 取消 | 执行 `sleep 300`，点取消，状态 `CANCELLED`，进程组被清理 |
| 9 | 超时 | `sleep 10` + `timeout_ms=2000`，状态 `TIMEOUT` |
| 10 | 断线重放 | 长任务执行中刷新浏览器，输出继续且不重复/不丢失（缓冲范围内） |
| 11 | 后端重启恢复 | 长任务中 `systemctl restart linux-ops-backend`，状态恢复为 `RUNNING` 并最终完成 |
| 12 | 幂等键 | 相同 `idempotency_key` 提交两次，返回同一任务，只执行一次 |
| 13 | Agent 重复 task_id | 直接对 Agent 重复 POST，同一任务不重复执行 |
| 14 | 日志落盘/下载 | 任务结束后 REST 读日志与下载文件均有内容 |
| 15 | 批量多机 | 至少两台受管主机同时执行，逐机结果正确 |
| 16 | systemd 单元 | `systemctl enable/start` 正常，重启后自启 |
| 17 | `install.sh` 全流程 | 在干净服务器上从零安装成功 |
| 18 | 前端完整交互 | 服务器 CRUD、标签、健康检查、历史筛选、日志查看 |
| 19 | MinIO | 用于 Agent 发布包（自更新源）；任务大日志未启用 |

## 三、Linux 验收步骤

### 3.1 基础

```bash
cd linux-ops-platform
sudo bash scripts/install.sh --lan-ip <私网IP>
sudo systemctl status linux-ops-agent linux-ops-backend --no-pager
curl -sS http://127.0.0.1:8089/api/v1/health
sudo bash tests/smoke.sh
```

### 3.2 取消

```bash
TASK=$(curl -sS -X POST http://127.0.0.1:8089/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"command":"sleep 300","timeout_ms":600000,"server_ids":[1]}')
echo "$TASK"
# 从返回中取 task.id，然后：
curl -sS -X POST http://127.0.0.1:8089/api/v1/tasks/<task-id>/cancel
sleep 2
curl -sS http://127.0.0.1:8089/api/v1/tasks/<task-id>
```

期望：子任务 `CANCELLED`；服务器上 `pgrep -af 'sleep 300'` 无残留。

### 3.3 超时

```bash
curl -sS -X POST http://127.0.0.1:8089/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"command":"sleep 30","timeout_ms":2000,"server_ids":[1]}'
```

期望：约 2 秒后状态 `TIMEOUT`。

### 3.4 后端重启恢复

```bash
# 提交长任务后立即重启后端
curl -sS -X POST http://127.0.0.1:8089/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"command":"for i in $(seq 1 30); do echo tick $i; sleep 1; done","timeout_ms":120000,"server_ids":[1]}'
sudo systemctl restart linux-ops-backend
sleep 5
journalctl -u linux-ops-backend -n 20 --no-pager
```

期望：日志出现 recovering，任务继续推进并最终 `SUCCESS`。
如果同时重启 Agent，则状态应为 `UNKNOWN`（不重复执行）。

### 3.5 幂等

```bash
KEY="test-idem-$(date +%s)"
for i in 1 2; do
  curl -sS -X POST http://127.0.0.1:8089/api/v1/tasks \
    -H 'Content-Type: application/json' \
    -d "{\"command\":\"date +%s%N >> /var/lib/linux-ops/idem-proof; echo done\",\"server_ids\":[1],\"idempotency_key\":\"$KEY\"}"
  echo
done
sudo wc -l /var/lib/linux-ops/idem-proof   # 应为 1
```

> 注意：当时 Agent 的 systemd 沙箱启用了 `PrivateTmp=true`，命令看到的 `/tmp`
> 与宿主机 `/tmp` 不共享，因此这里用 `/var/lib/linux-ops` 做证明文件。当前部署
> 已改为 root 运行且不再启用 `PrivateTmp`，该验证结论本身仍成立。

### 3.6 更新本文档

完成一项就把对应行从「待验证」移到「已验证」，附上命令与结果。
**不要**在没有执行的情况下勾选完成。

## 四、2026-11-06 增量验证：文件管理 + Agent 在线升级

环境：后端运行在 Windows 开发机（连接 `47.99.2.212:3306` 的 MySQL），
受管主机 `47.99.2.212:19090`（Linux amd64，root 运行，平台登记为
`aliyun-linux-agent`，server id=2）。

| # | 项目 | 验证方式 | 结果 |
|---|---|---|---|
| 1 | 文件浏览 | `GET /servers/2/fs/list?path=/` | 通过，返回 24 项，dir/file/symlink 分类与 mode/uid/gid 正确 |
| 2 | 新建目录/文件 | `POST /servers/2/fs/op` mkdir / touch | 通过 |
| 3 | 编辑保存 | `PUT /fs/write` + `GET /fs/read` | 通过，内容一致 |
| 4 | 并发冲突 | 使用旧 `base_mtime` 再次保存 | 通过，返回 409 |
| 5 | 分片上传 | 2 片 `PUT /fs/upload` | 通过，`received/done` 正确，落盘内容一致 |
| 6 | 下载与断点 | `GET /fs/download`，含 `Range: bytes=6-14` | 通过，返回 206 与正确片段 |
| 7 | 重命名 | `POST /fs/op` rename | 通过 |
| 8 | 递归删除 | `POST /fs/op` delete 目录 | 通过，测试目录已清理 |
| 9 | 审计 | `GET /audit` | 通过，含 fs.write / fs.upload / fs.rename / fs.download / agent.update |
| 10 | Agent 自升级 | `POST /servers/2/agent/update` | 通过：0.1.1 → 0.1.2，Agent 自行拉包、校验、替换并重启；随后健康检查 `version=0.1.2` |
| 11 | 升级备份 | 列 `/opt/linux-ops-platform/bin` | 存在 `agent.old` |
| 12 | 升级后版本判断 | `GET /servers/2/agent/update/check` | `available=false`，current=latest=0.1.2 |
| 13 | 单元测试 | `go test ./internal/api ./internal/update` | 通过（文件生命周期、mtime 冲突、路径守卫、版本比较） |
| 14 | 前端构建 | `npm run build` | 通过 |

未验证 / 待确认：

- 浏览器中实际点击「文件管理」「检查更新 / 批量升级」的交互（后端接口已
  实测，前端只验证了构建）。
- 多台主机批量升级（当前只有一台在线主机）。
- 0.1.0 → 0.1.1/0.1.2 的首次引导：0.1.0 没有升级接口，必须手动执行一次
  安装脚本；之后才能使用页面统一升级。
- 升级信任模型：manifest 与包同源、仅做 SHA256 校验，未做签名；MinIO 写
  权限泄露等同于对所有 Agent 的 root 代码执行。

## 五、2026-11-06 UI：黑白主题切换 + 单机执行页

环境同上一节：后端 `127.0.0.1:8089`，Vite `127.0.0.1:5173`，受管主机
`47.99.2.212`（server id=2）。

| # | 项目 | 验证方式 | 结果 |
|---|---|---|---|
| 1 | 主题切换 | 无头 Chrome 真实点击 `.theme-toggle` | 通过：`data-theme` dark → light，`localStorage.lops-theme=light` |
| 2 | 主题持久化 | 预置 `lops-theme=light` 后加载页面 | 通过：整站以浅色渲染 |
| 3 | 双主题渲染 | 无头 Chrome 截图（单机页/服务器页） | 通过：黑白配色、输入框、表格、按钮、状态徽标均正常 |
| 4 | 固定列遮挡 | 截图核对服务器表「操作」列 | 通过：修复前固定列透明、内容穿透；修复后不透明（需横向滚动查看中间列） |
| 5 | 单机页跳转 | 无头 Chrome 点击命令执行页主机名 | 通过：hash 变为 `#/servers/2/execute`，标题「单机执行」，服务器名/状态/Agent 版本正确 |
| 6 | 单机页数据 | 截图/接口 | 通过：服务器详情、快捷命令、最近任务（该主机历史）加载正常 |
| 7 | 入口 | 代码 + 渲染 | 命令执行页主机名链接、行点击（复选框不触发）、服务器管理「执行」按钮 |
| 8 | 前端构建 | `npm run build` | 通过（370 modules） |

未验证 / 待确认：

- 浅色主题下未逐一截图全部 Element 组件（日期选择器、抽屉、消息提示等），
  仅覆盖了本页用到的表格/表单/按钮/标签。
- 单机页的执行、取消、重试走的是与批量执行相同且已实测的任务接口，
  本页只做了真实点击跳转与渲染验证，未再重复执行真实命令。

## 六、2026-11-06 配置集中化（configs/local.yaml）

MySQL 与 MinIO 连接信息统一到 `configs/local.yaml`（含口令，已加入
`.gitignore`）；后端以该文件为唯一 `-config`，不再依赖启动环境变量。

| # | 项目 | 验证方式 | 结果 |
|---|---|---|---|
| 1 | 单文件启动 | 仅执行 `./bin/backend.exe -config configs/local.yaml`（无 `DB_*` 环境变量） | 通过，`/api/v1/health` 200 |
| 2 | MySQL 连接 | 读取该文件连接 47.99.2.212 | 通过，`/servers` 返回 server 2 且心跳持续更新 |
| 3 | 真实任务 | `POST /tasks` → Agent 执行 `echo` + `uname -s` | 通过，`SUCCESS`、退出码 0、stdout 正确 |
| 4 | MinIO 凭据 | `bin/minio_upload.py` 读取同一文件，`HEAD /linux-ops-agent` | 通过，HTTP 200 |
| 5 | 前端代理 | `GET http://127.0.0.1:5173/api/v1/health` | 通过，200 |
| 6 | 防误提交 | `git check-ignore -v configs/local.yaml` | 通过，命中 `.gitignore:14` |
| 7 | 构建与测试 | `gofmt -l . && go build ./... && go vet ./... && go test ./...` | 通过 |

未验证 / 边界：

- Linux 一键部署路径仍使用 `deploy/.env` + `/etc/linux-ops/backend.env`，
  未改为读取 `configs/local.yaml`（两者不冲突，部署侧未改）。
- 远端 Agent 的 `agent.yaml` 在目标主机上，无法并入本机文件；其中
  `update_base_url` 需与 `local.yaml` 的 `minio.update_base_url` 保持一致。

## 七、2026-11-06 前端自定义部署前缀（VITE_BASE_PATH + VITE_API_BASE）

前端入口与后端请求前缀可以不同：

- `VITE_BASE_PATH`：静态资源/入口挂载路径（默认 `/`）
- `VITE_API_BASE`：浏览器访问后端的完整基址，REST、下载链接、WebSocket
  共用（默认 `<base>api/v1`）

| # | 项目 | 验证方式 | 结果 |
|---|---|---|---|
| 1 | 默认构建不变 | `npm run build` | 通过，index.html 引用 `/assets/...`，包内 API base 为 `/api/v1` |
| 2 | 双前缀构建 | `VITE_BASE_PATH=ops-wxgzt VITE_API_BASE=ops-server/api/v1 npm run build` | 通过，产物为 `dist/ops-wxgzt/index.html`，引用 `/ops-wxgzt/assets/...`，包内 axios/ws 基址为 `/ops-server/api/v1` |
| 3 | 产物目录带前缀 | `find dist` | 通过，只有 `dist/ops-wxgzt/{index.html,assets}`，没有平铺的 `dist/index.html` |
| 4 | 构建自动清理 | 先默认构建再前缀构建（及反向） | 通过，每次 `dist` 只保留本次布局，切换前缀不残留旧产物 |
| 5 | 前缀开发服务器 | 同参数 `npx vite --port 5174` | 通过，`/ops-wxgzt/` 返回 200，脚本路径 `/ops-wxgzt/@vite/client` |
| 6 | 注入值 | 读取 dev server 转换后的 `base.js` | 通过，`BASE_URL=/ops-wxgzt/`、`VITE_API_BASE=ops-server/api/v1`（运行时补前导 `/`） |
| 7 | 开发代理去前缀 | `GET :5174/ops-server/api/v1/health` | 200，剥掉 `/ops-server/api/v1` 后转发为后端 `/api/v1/health` |
| 8 | 无前缀回归 | 原有 5173 dev server、`:8089` health | 200，未受影响 |

未验证 / 边界：

- 未在本机执行真实 Nginx（Windows 开发机无 Nginx）；`deploy/nginx-prefix.conf.example`
  只做了配置编写，需在 Linux 上用 `nginx -t` 与浏览器实测。
- 前缀模式下 WebSocket 未跑真实长任务；已确认包内 WS 与 REST 共用同一
  `API_BASE`（`wsUrl()` 与 axios 的 `baseURL` 是同一个常量）。
- 后端内置静态托管（`frontend_dir`）仍假设根路径 `/`；使用前缀时必须由
  Nginx 提供静态文件，二者不要混用。
- 构建开始时 `clean-dist` 插件会删除整个 `dist/`；如果生产环境直接把
  `dist/` 作为静态目录，构建期间会有短暂缺文件，应在发布流程中先构建再同步。
- Nginx 示例中的静态路径按“dist 里带前缀目录”编写
  （`frontend/ops-wxgzt/`），与新的产物结构一致。
- **Windows Git Bash/MSYS 会把 `VITE_BASE_PATH=/ops-wxgzt/` 改写为
  `C:/Program Files/Git/ops-wxgzt/`**，导致白屏；应去掉前导斜杠或使用
  `frontend/.env.local`（已实测复现并给出规避方式）。

## 八、2026-11-06 Agent 0.1.5：按桶扫描发现新版本（免手工 manifest）

背景：此前每次发版都要手改 `manifest.json` 的版本与 SHA256，容易漏改或写错，
漏改时全平台都检测不到新版本。0.1.5 改为以桶内文件名为准。

实现（`agent/internal/update`）：

- `resolve()` 同时尝试两条来源，取版本较高者：
  - `ListObjectsV2`（匿名，`prefix=linux-ops-agent-`）解析
    `linux-ops-agent-<x.y.z>-linux-<GOARCH>.tar.gz`；
  - `manifest.json`（可选，提供 notes 与 sha256）。
- 校验和优先级：manifest → `SHA256SUMS` → 无（默认放行，
  `update_require_checksum: true` 时拒绝）。
- `/api/v1/update/check` 新增 `source`（manifest / listing / manifest+listing）
  与 `verified`，平台 UI 在结果表里显示「发现来源」和「已校验/未校验」。
- 桶策略由 `bin/minio_upload.py` 统一设置为匿名 `s3:GetObject` + `s3:ListBucket`；
  脚本根据 tar.gz 文件名自动生成 `manifest.json` 与 `SHA256SUMS`。

验证（真实主机 47.99.2.212，server id=2）：

| # | 场景 | 结果 |
|---|---|---|
| 1 | 0.1.4 → 0.1.5 页面升级（旧 manifest 路径） | 成功，`/info` 返回 0.1.5 |
| 2 | 桶内出现 0.1.6、manifest 仍停在 0.1.5 | 检查返回 `latest=0.1.6, available=true, source=listing, verified=false` |
| 3 | 仅给 0.1.6 补 `SHA256SUMS` 条目 | `verified` 变为 true，仍为 `source=listing` |
| 4 | 删除 0.1.6、恢复 SHA256SUMS | 回到 `latest=0.1.5, available=false` |
| 5 | **删除 manifest.json** 后检查 | 仍返回 `latest=0.1.5, source=listing, verified=true` |

未验证 / 边界：

- 场景 2/3/5 只验证了「发现」，未用 listing 来源真正执行一次升级下载；下载与
  解包路径与 manifest 模式共用（`download(file)` + `extractBinary`）。
- 匿名 ListBucket 使桶内容可被枚举；隔离网络内可接受，公网部署应改为私有桶
  + 签名或走内网制品服务。
- 文件名扫描的固有风险：桶里出现一个命名正确但损坏/错误的包会被当作候选；
  建议发布只用 `bin/minio_upload.py`，并保留 `SHA256SUMS`。
- 仍未实现「后台定时发现 + 列表角标」，检测仍由页面手动触发。

## 九、多用户登录与服务器归属隔离（2026-11-11）

实现（后端 `auth.go` / `users.go` / `permissions.go`、前端 `Users.vue`）：

- 账号：`users` 表，密码只存 bcrypt；角色 `admin` / `user`。首次启动自动创建
  `admin`，初始密码取 `auth.access_password`（空则 `changeme`），之后配置项
  不再覆盖数据库密码。
- 会话：`user_sessions` 表只存 Cookie Token 的 SHA-256，HttpOnly Cookie
  `lop_session`，有效期 `auth.session_hours`（默认 12 小时）。改密/重置密码
  会撤销该用户全部旧会话，并为当前浏览器重新签发。
- 接口：`POST /auth/login`（用户名+密码）、`POST /auth/logout`、
  `POST /auth/password`（自助改密）、`/users` 下的列表/创建/重置/删除
  （仅管理员）。登录失败按 IP 限流（10 次/分钟）。
- 归属：`servers` 与 `execution_tasks` 增加 `owner_id` / `owner_name`；
  子用户只能看到和操作自己名下的服务器、任务、日志、文件、终端与 WebSocket；
  管理员可见全部。分组/标签为全局配置，只有管理员能增删改。
- 历史数据：启动时把 `owner_id=0` 的服务器与任务回填给 `admin`。
- 前端：登录页改为用户名+密码；顶栏显示当前用户与角色、修改密码、退出；
  管理员多出「用户管理」页；服务器列表显示「归属账号」标识；子用户隐藏
  分组/标签管理入口。

验证（本机 127.0.0.1:8089 + 真实 MySQL，`admin/changeme`）：

| # | 场景 | 结果 |
|---|---|---|
| 1 | 未登录访问 `/auth/status`、`/servers` | `authenticated:false`；401 |
| 2 | `admin/changeme` 登录 | 200，下发 `lop_session` |
| 3 | 管理员创建子用户 `tester01` | 200，`role=user` |
| 4 | 子用户登录后列出服务器 | 空列表（看不到管理员的服务器） |
| 5 | 子用户新增服务器 | 成功，`owner_id/owner_name` 为子用户 |
| 6 | 管理员列出服务器 | 两台都可见，归属分别为 `admin` / `tester01` |
| 7 | 子用户访问 `/users` | 403 |
| 8 | 子用户直接访问/删除管理员服务器 | 403 |
| 9 | 子用户自助改密后旧会话 | 旧会话失效，新 Cookie 可用，旧密码登录 401 |
| 10 | 管理员重置子用户密码 / 删除子用户 | 成功；名下仍有服务器时删除被拒绝 |

未覆盖 / 边界：

- 链路仍是明文 HTTP：账号密码和 Cookie 在内网可被抓包；没有 TLS/MFA。
- 权限只有 admin/user 两级 + 归属过滤，没有细粒度 RBAC；子用户对自己名下
  服务器拥有完整命令执行权限。
- Agent 19090 端口无认证，拿到端口访问权即可绕过平台。
- 审计记录带用户名，但数据库权限足够的人仍可修改；需外部日志留证。

## 十、JWT 与 API 令牌（2026-11-11）

实现（后端 `jwt.go` / `tokens.go` / `auth.go`、前端 `App.vue` / `Users.vue`）：

- 统一鉴权：`requireAuth` 先看 `Authorization: Bearer`，再看 `lop_session`
  Cookie。Bearer 为 `lop_` 开头时按 API 令牌查表；否则按 HS256 JWT 校验。
- JWT：`POST /auth/token` 用用户名/密码换取（复用登录限流与失败延迟），
  claims 含 `sub/usr/role/pv/iss/iat/exp`，有效期 `auth.jwt_minutes`（默认 120，
  上限 1440）。`pv` 是密码哈希指纹，改密/重置后旧 JWT 立即 401。未配置
  `auth.jwt_secret` 时首次启动自动生成并存入 `app_settings`，重启后仍可验证。
- API 令牌：`GET/POST /auth/tokens`、`DELETE /auth/tokens/{id}`。格式
  `lop_<43 字符>`，数据库只存 SHA-256 与展示前缀，明文仅创建响应出现一次；
  可设 0–3650 天过期（0 = 永不过期），每人最多 20 个；管理员可查看/吊销他人
  令牌；`last_used_at` 最多每 5 分钟更新。管理员重置密码或删除账号时吊销该用户
  全部令牌；自助改密不吊销令牌。
- 审计：`auth.token.issue`、`auth.token.failed`、`token.create`、`token.revoke`。
- 前端：顶栏新增「令牌」对话框（创建、一次性明文复制、吊销、过期/最后使用）；
  管理员在用户管理页可查看并吊销子用户令牌。

验证（本机 127.0.0.1:8089 + 真实 MySQL）：

| # | 场景 | 结果 |
|---|---|---|
| 1 | `POST /auth/token` 换取 JWT，Bearer 访问 `/users` | 200，返回管理员数据 |
| 2 | 错误密码换取 JWT | 401，不泄露用户名是否存在 |
| 3 | 篡改 JWT 签名 | 401 |
| 4 | 无凭证访问受保护接口 | 401 |
| 5 | 用 JWT 创建 API 令牌，Bearer 访问 `/servers` | 200 |
| 6 | 吊销 API 令牌后再次访问 | 401（列表删除，立即生效） |
| 7 | 子用户 JWT 列出服务器 | 空列表，归属隔离仍生效 |
| 8 | 子用户自助改密后旧 JWT | 401 |
| 9 | 子用户自助改密后其 API 令牌 | 仍可用（设计如此） |
| 10 | 管理员重置该用户密码后其 API 令牌 | 401，且令牌记录被删除 |
| 11 | 管理员 `?user_id=N` 查看他人令牌 / 删除用户 | 200 / 200 |

未覆盖 / 边界：

- JWT 与 API 令牌都是明文 HTTP 传输，内网可被抓包；没有 TLS、mTLS 或令牌 scope。
- JWT 无刷新令牌，过期需重新调用 `/auth/token`；退出登录只撤销 Cookie 会话。
- API 令牌权限完全等同创建者账号，不能限制为只读或限定服务器范围。
- Bearer 仅用于 REST；浏览器 WebSocket 仍依赖 Cookie（脚本暂不能建 WS）。

## 十一、生产部署、Agent 白名单与混淆构建（2026-11-11）

实现：

- Agent 新增 `allowed_ips`（IP/CIDR 列表），所有 REST/WebSocket 请求在路由前按
  TCP 对端地址过滤，其他来源返回 403；不信任 `X-Forwarded-For`。
- `config.Validate` 拒绝 `listen_addr` 为 `0.0.0.0`/`::`，拒绝空白名单和
  `0.0.0.0/0`、`::/0`、`*` 等通配项；安装脚本同样在下载前拒绝。
- 安装脚本重写：固定安装到 `/opt/wxgzt-ops`，服务名 `wxgzt-ops`，服务以
  root 运行（不创建系统用户），配置 `/etc/wxgzt-ops/agent.yaml`；新增 `--base-url`、
  `--listen-addr`、可重复的 `--allow-ip`；已预置配置文件时可直接用
  `--config`，不再要求白名单参数；校验 SHA256；自动停用旧
  `linux-ops-agent`；运行期不写日志文件，仅错误进 journald。
- 发布命名统一为 `wxgzt-ops-agent-<ver>-linux-<arch>.tar.gz`；自更新同时兼容新
  布局和旧包内的 `agent` 文件名；多架构桶中的 `manifest.json` 只对匹配架构
  生效，避免把 amd64 包下发到 arm64。
- 新增 `scripts/build-release.sh`：garble 字面量混淆 + `-trimpath` +
  `-s -w`，同时构建后端与 Agent 的 linux amd64/arm64；生成 `SHA256SUMS`。
- systemd unit 改为 `wxgzt-ops.service`、`wxgzt-ops-backend.service`；
  Agent unit 设置 `StandardOutput=null`、`StandardError=journal` 并限速。
- 后端新增 `MINIO_*` 环境变量覆盖（当前后端不直连 MinIO，变量与发布工具共用）。
- 新增独立文档 `docs/production-deploy.md`（nginx + 现有 MySQL/MinIO、
  Agent 白名单安装、升级回滚、排障与安全清单）。

验证：

| # | 场景 | 结果 |
|---|---|---|
| 1 | `go test ./...`（agent，含白名单/配置校验/更新解析） | 全部通过 |
| 2 | `go test ./...`（backend） | 全部通过 |
| 3 | `allowed_ips` 放行/拒绝、伪造 `X-Forwarded-For` | 单测通过：白名单外 403，XFF 不影响判定 |
| 4 | `listen_addr=0.0.0.0`、空白名单、`0.0.0.0/0` | 配置校验返回错误 |
| 5 | garble 构建 linux amd64/arm64 Agent 与后端 | 成功；`file` 显示 stripped ELF；二进制内无 `linux-ops-platform` 字符串 |
| 6 | 发布包结构与 `SHA256SUMS` | `wxgzt-ops/{wxgzt-ops,agent.yaml,wxgzt-ops.service,agent-install.sh,README.md}`；校验和可解析 |
| 7 | `bash -n` 安装/构建/安装器脚本 | 通过 |
| 8 | 前端 `npm run build` | 成功，产物 `dist/ops-wxgzt/` |

待验证（需真实 Linux 主机）：

- 用 `curl | sudo bash` 实际安装，确认 systemd 启动、403 白名单、后端可访问。
- 在 MinIO 上验证多架构发布、Agent 自更新与 `SHA256SUMS` 链路。
- nginx 前缀反代 + WebSocket 的端到端验证。

未覆盖 / 边界：

- `allowed_ips` 是网络层兜底，不是请求认证；能伪造源 IP 或控制白名单主机的人
  仍可执行命令。仍需防火墙/安全组。
- garble 只提高反编译成本，不能把可执行 Go 程序真正“加密”。
- 后端仍不直连 MinIO（`MINIO_*` 目前供发布工具和后续版本使用）。

## 零参数安装与桶精简（2026-11-11）

变更：

- Agent 桶只保留 `wxgzt-ops-agent-<ver>-linux-<arch>.tar.gz` 和
  `agent-install.sh`；不再上传 `manifest.json` / `SHA256SUMS`。
- `bin/minio_upload.py` 渲染安装脚本时同时写入桶地址和本地 `SHA256SUMS`
  内容，安装器下载后仍按 SHA256 校验安装包。
- 安装器默认把包内 `agent.yaml` 安装为 `/opt/wxgzt-ops/agent.yaml`，systemd
  unit 也改从这里读取；已有文件默认保留，`--force` 才覆盖，旧
  `/etc/wxgzt-ops/agent.yaml` 自动迁移。
- 包内 `update_base_url` 为回环占位值时，安装阶段改写为实际制品桶地址。
- `curl -fsSL <url>/agent-install.sh | sudo bash` 不需要任何参数；`--config`、
  `--listen-addr`、`--allow-ip` 仍作为可选项保留。

验证：

| # | 场景 | 结果 |
|---|---|---|
| 1 | `bash -n` 安装器/构建/安装脚本、Python 语法 | 通过 |
| 2 | 模拟上传渲染 | 脚本内出现实际桶地址和两条构件的 SHA256；`bash -n` 通过 |
| 3 | Agent 包内容 | `agent.yaml`、`agent-install.sh`(0755)、service、README、二进制齐全 |

已知边界：

- 桶里没有 `SHA256SUMS` 时，Agent 自更新无法比对发布校验和，包内配置使用
  `update_require_checksum: false`；需要严格校验时必须额外保留 `SHA256SUMS`
  并发安装包后重跑 `bin/minio_upload.py`。

## 后端混淆导致表名不匹配（2026-11-11）

现象：手动执行 `database/schema.sql` 后启动后端，报
`select * from rd_yvyos` 之类的表不存在。

原因（已用 garble/plain 对比构建验证）：

- garble 会重命名导出的 Go 类型名和结构体字段名；
- GORM 通过反射从这些标识符推导表名/列名，因此混淆后的后端会去查随机表名，
  与手动创建的标准表名完全不一致。这不是数据库编码问题；`utf8mb4` 设置正确，
  表名是纯 ASCII，编码错误不会把 `servers` 变成 `rd_yvyos`。

修复：

- `scripts/build-release.sh` 只对 Agent 使用 garble，后端改为普通
  `go build -trimpath -s -w`；
- 为 `Setting` / `ExecutionLogIndex` 增加显式 `TableName()`，分别固定为
  `app_settings` / `execution_log_indexes`，使 AutoMigrate 与 `schema.sql`
  完全一致（默认命名会生成 `settings` / `execution_log_indices`）；
- 验证：用反射程序对比混淆/普通构建的表名与列名；修复后 12 张表、全部列名与
  `database/schema.sql` 一致。

已污染数据：若曾用混淆后端配 `auto_migrate=true` 启动，数据库中会出现
`k_jv_ue_mg_lzs` 这类随机表；切换到普通构建前应删掉这些表，或直接在空库上重新
执行 `schema.sql`。

## Agent 监听地址自动化（2026-11-13）

问题：`agent.yaml` 固定 `listen_addr: "127.0.0.1"`，跨机部署时每台都要手改成
该主机的内网 IP，容易漏改或填错。

方案：`listen_addr` 支持 `auto`，并作为默认值：

- Agent 启动时自动选择非回环 IPv4：优先取默认路由的源地址（UDP dial 只做
  路由选择，不发包）；该地址不是 RFC1918 时，优先找物理网卡上的私有地址，
  跳过 `docker0`/veth/`br-`/`virbr`/tun 等虚拟网卡；
- 仍然是具体 IP 绑定，不监听 `0.0.0.0`/`::`，`allowed_ips` 校验不变；
- 选中的地址在启动时写一行 journal：`listen_addr: auto -> 10.x.x.x`；
- 安装脚本的 `--listen-addr` 不传时写 `auto`，健康探测按同样的路由规则取
  实际地址；显式传 IP 时行为不变；
- `configs/agent.yaml`、安装包内配置、文档同步更新。

验证：

| # | 场景 | 结果 |
|---|---|---|
| 1 | `gofmt` / `go build` / `go vet` / `go test ./...`（agent） | 通过 |
| 2 | `configs/agent.yaml`（`listen_addr: auto`）经 `config.Load` | 解析出具体非回环地址并通过校验 |
| 3 | 自动探测函数实测 | 返回本机主用内网地址（192.168.x.x） |
| 4 | `bash -n` 安装脚本；`bin/agent-install.sh` 同步 | 通过 |
| 5 | 重新构建 agent/backend/server/frontend 包 | `sha256sum -c` 全部 OK；安装包内 `agent.yaml` 为 `auto` |

注意：旧版 Agent 二进制不认识 `auto`，升级时必须同时替换二进制与配置。
