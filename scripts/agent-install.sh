#!/usr/bin/env bash
# wxgzt-ops Agent 一键安装脚本（可直接 curl | sudo bash 执行）。
#
# 制品桶里只放两类对象：
#   wxgzt-ops-agent-<x.y.z>-linux-<arch>.tar.gz   安装包
#   agent-install.sh                              本脚本（上传时写入桶地址和
#                                                 安装包 SHA256）
#
# 最简用法（不需要任何参数）：
#   curl -fsSL https://<minio>/<bucket>/agent-install.sh | sudo bash
#   # 自签名 HTTPS 证书：外层 curl 需要自己加 -k
#
# 脚本流程：下载并校验安装包 -> 解压 -> 安装到 /opt/wxgzt-ops ->
# 直接使用包内的 /opt/wxgzt-ops/agent.yaml -> 注册并启动 systemd 服务。
# 目标机已有 /opt/wxgzt-ops/agent.yaml 时默认保留；旧部署的
# /etc/wxgzt-ops/agent.yaml 会自动迁移过来。
#
# 可选参数（通常不需要）：
#   --config <path>        使用指定的完整 agent.yaml 替换安装目录中的配置
#   --listen-addr <IP|auto> 生成新配置时使用（默认使用包内 agent.yaml，
#                          其 listen_addr 为 auto）
#   --allow-ip <IP|CIDR>   生成新配置时使用，可重复或逗号分隔
#   --base-url <URL>       制品桶地址（默认使用脚本内写入的地址）
#   --version <x.y.z>      指定版本；默认选桶内该架构最高版本
#   --port <port>          仅生成新配置时使用，默认 19090
#   --force                用包内 agent.yaml 覆盖已有配置
#
# HTTPS 制品桶：
#   当制品桶地址以 https:// 开头时，脚本内部下载默认加 -k 跳过证书校验
#   （内网 MinIO 常用自签名证书）；需要强制校验证书时设置
#   AGENT_TLS_VERIFY=1。注意外层 curl 下载本脚本不受此控制，自签名证书下
#   需要写成 curl -k -fsSL ... | sudo bash。
#
# 环境变量等价形式：AGENT_BASE_URL / AGENT_ALLOW_IPS / AGENT_LISTEN_ADDR /
# AGENT_CONFIG / AGENT_VERSION / AGENT_FORCE / AGENT_TLS_VERIFY。
#
# 安装位置固定为 /opt/wxgzt-ops，配置 /opt/wxgzt-ops/agent.yaml，
# systemd 服务名 wxgzt-ops。脚本不读取 stdin，可安全通过管道执行。
set -euo pipefail

# bin/minio_upload.py 在上传时把这些占位符替换成实际的 ENDPOINT/BUCKET 和
# 本地构建产物的 SHA256SUMS 内容，因此桶里不需要再放 SHA256SUMS 文件。
# 手工 mc cp 上传的副本没有经过替换：地址回落到生产默认值，校验和则回退到
# 从桶里读取 SHA256SUMS/manifest.json。
DEFAULT_BASE_URL="__WXGZT_AGENT_BASE_URL__"
case "$DEFAULT_BASE_URL" in
  __WXGZT_AGENT_BASE_URL__*) DEFAULT_BASE_URL="https://minio.internal:9000/wxgzt-ops" ;;
esac
EMBEDDED_SUMS='__WXGZT_AGENT_SHA256SUMS__'
if [ "$EMBEDDED_SUMS" = "__WXGZT_AGENT_SHA256SUMS__" ]; then
  EMBEDDED_SUMS=""
fi

BASE_URL="${AGENT_BASE_URL:-}"
LISTEN_ADDR="${AGENT_LISTEN_ADDR:-}"
ALLOW_SPEC="${AGENT_ALLOW_IPS:-}"
LISTEN_PORT="${AGENT_LISTEN_PORT:-19090}"
VERSION_ARG="${AGENT_VERSION:-}"
FORCE="${AGENT_FORCE:-0}"
CONFIG_SRC="${AGENT_CONFIG:-}"
ALLOW_IPS=()

INSTALL_DIR="/opt/wxgzt-ops"
CFG_FILE="$INSTALL_DIR/agent.yaml"
LEGACY_CFG="/etc/wxgzt-ops/agent.yaml"
CONF_DIR="/etc/wxgzt-ops"
SERVICE="wxgzt-ops"

log() { printf '[wxgzt-ops] %s\n' "$*"; }
die() { printf '[wxgzt-ops] 错误：%s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
用法：curl -fsSL <url>/agent-install.sh | sudo bash
      （不需要其他参数：安装包内自带 agent.yaml）

可选：
  --base-url     制品桶地址（默认使用脚本内置地址，如
                 https://minio.internal:9000/wxgzt-ops）
  --config       使用指定的完整 agent.yaml 替换安装目录中的配置
  --listen-addr  生成新配置时使用的本机监听地址（不传则写 auto，
                 由 Agent 启动时自动探测主用内网 IP）
  --allow-ip     生成新配置时使用的后端来源地址（可重复/逗号分隔）
  --version      指定版本（默认选桶内最高版本）
  --port         生成新配置时的监听端口（默认 19090）
  --force        用包内 agent.yaml 覆盖已有 /opt/wxgzt-ops/agent.yaml

默认配置：/opt/wxgzt-ops/agent.yaml（来自安装包），systemd 服务 wxgzt-ops。
HTTPS 制品桶默认用 curl -k 跳过证书校验；强制校验设 AGENT_TLS_VERIFY=1。
自签名证书下外层下载脚本也要加 -k：curl -k -fsSL <url> | sudo bash
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --listen-addr) LISTEN_ADDR="${2:-}"; shift 2 ;;
    --listen-addr=*) LISTEN_ADDR="${1#*=}"; shift ;;
    --allow-ip) ALLOW_SPEC="${ALLOW_SPEC} ${2:-}"; shift 2 ;;
    --allow-ip=*) ALLOW_SPEC="${ALLOW_SPEC} ${1#*=}"; shift ;;
    --base-url) BASE_URL="${2:-}"; shift 2 ;;
    --base-url=*) BASE_URL="${1#*=}"; shift ;;
    --config) CONFIG_SRC="${2:-}"; shift 2 ;;
    --config=*) CONFIG_SRC="${1#*=}"; shift ;;
    --version) VERSION_ARG="${2:-}"; shift 2 ;;
    --version=*) VERSION_ARG="${1#*=}"; shift ;;
    --port) LISTEN_PORT="${2:-}"; shift 2 ;;
    --port=*) LISTEN_PORT="${1#*=}"; shift ;;
    --force) FORCE=1; shift ;;
    -h | --help) usage; exit 0 ;;
    # 旧版用法把监听地址作为位置参数；现在必须显式指定白名单或 --config。
    -*) die "未知参数：$1（--help 查看用法）" ;;
    *) die "不再接受位置参数「$1」；直接 curl .../agent-install.sh | sudo bash 即可使用包内 agent.yaml" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "需要 root 权限：curl .../agent-install.sh | sudo bash"

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "不支持的架构：$(uname -m)" ;;
esac

for cmd in curl tar sha256sum systemctl install awk; do
  command -v "$cmd" >/dev/null 2>&1 || die "缺少命令：$cmd"
done

detect_listen_addr() {
  local ip=""
  if command -v ip >/dev/null 2>&1; then
    ip="$(ip -4 route get 1.1.1.1 2>/dev/null |
      awk '{ for (i = 1; i <= NF; i++) if ($i == "src") { print $(i + 1); exit } }')"
  fi
  if [ -z "$ip" ] && command -v hostname >/dev/null 2>&1; then
    ip="$(hostname -I 2>/dev/null | awk '{ print $1 }')"
  fi
  printf '%s' "$ip"
}

BASE_URL="${BASE_URL:-$DEFAULT_BASE_URL}"
[ -n "$BASE_URL" ] || die "无法确定制品桶地址：请用 --base-url 或 AGENT_BASE_URL 指定"

# ---- 2. 选择版本 ----
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

BASE_URL="${BASE_URL%/}"

# 内网 MinIO 常用自签名证书：https 源默认加 -k。AGENT_TLS_VERIFY=1 可强制校验。
CURL_K=""
if [ "${AGENT_TLS_VERIFY:-0}" != "1" ]; then
  case "$BASE_URL" in
    https://*)
      CURL_K="-k"
      log "HTTPS 制品源：内部 curl 使用 -k 跳过证书校验（AGENT_TLS_VERIFY=1 可强制校验）"
      ;;
  esac
fi
fetch() {
  if [ -n "$CURL_K" ]; then
    curl -fsSL -k "$@"
  else
    curl -fsSL "$@"
  fi
}

if [ -n "$VERSION_ARG" ]; then
  VERSION="$VERSION_ARG"
else
  VERSION="$(fetch --retry 3 --max-time 30 \
    "$BASE_URL/?list-type=2&prefix=wxgzt-ops-agent-" |
    grep -oE "wxgzt-ops-agent-[0-9]+\.[0-9]+\.[0-9]+-linux-${ARCH}\.tar\.gz" |
    sort -V | tail -n 1 |
    sed -E 's/^wxgzt-ops-agent-([0-9]+\.[0-9]+\.[0-9]+)-.*$/\1/' || true)"
  [ -n "$VERSION" ] || die "制品桶中找不到 wxgzt-ops-agent-*-linux-${ARCH}.tar.gz"
fi
PKG="wxgzt-ops-agent-${VERSION}-linux-${ARCH}.tar.gz"

# ---- 3. 校验和（脚本内置优先，其次桶内 SHA256SUMS/manifest.json）----
# minio_upload.py 上传时会把本次构建的 SHA256SUMS 写进脚本，所以桶里只需要
# 安装包和 agent-install.sh 两个对象；手工 mc cp 的副本仍回退到桶内文件。
SHA256=""
if [ -n "$EMBEDDED_SUMS" ]; then
  SHA256="$(printf '%s\n' "$EMBEDDED_SUMS" |
    awk -v f="$PKG" '$2 == f || $2 == "*" f { print $1; exit }')"
fi
if [ -z "$SHA256" ] && fetch --retry 3 --max-time 30 "$BASE_URL/SHA256SUMS" -o "$TMP/SHA256SUMS"; then
  SHA256="$(awk -v f="$PKG" '$2 == f || $2 == "*" f { print $1; exit }' "$TMP/SHA256SUMS")"
fi
if [ -z "$SHA256" ] && fetch --retry 3 --max-time 30 "$BASE_URL/manifest.json" -o "$TMP/manifest.json"; then
  MFILE="$(sed -n 's/.*"file"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$TMP/manifest.json" | head -n 1)"
  if [ "$MFILE" = "$PKG" ]; then
    SHA256="$(sed -n 's/.*"sha256"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$TMP/manifest.json" | head -n 1)"
  fi
fi
if [ -z "$SHA256" ]; then
  if [ "${AGENT_ALLOW_NO_CHECKSUM:-0}" = "1" ]; then
    log "警告：缺少校验和，按 AGENT_ALLOW_NO_CHECKSUM=1 继续"
  else
    die "没有 $PKG 的 SHA256；拒绝安装未校验的二进制（可设 AGENT_ALLOW_NO_CHECKSUM=1 强制）"
  fi
fi

# ---- 4. 下载并校验 ----
fetch --retry 3 --max-time 300 "$BASE_URL/$PKG" -o "$TMP/$PKG"
if [ -n "$SHA256" ]; then
  printf '%s  %s\n' "$SHA256" "$PKG" | (cd "$TMP" && sha256sum -c - >/dev/null) ||
    die "SHA256 校验失败"
fi

# ---- 5. 解包 ----
tar xzf "$TMP/$PKG" -C "$TMP"
BIN_SRC=""
for cand in "$TMP/wxgzt-ops/wxgzt-ops" "$TMP/linux-ops-agent/agent"; do
  if [ -f "$cand" ]; then BIN_SRC="$cand"; break; fi
done
[ -n "$BIN_SRC" ] || die "包结构异常：缺少 wxgzt-ops/wxgzt-ops"
chmod 0755 "$BIN_SRC"

# ---- 6. 安装（服务以 root 运行，不创建系统用户）----
PKG_DIR="$TMP/wxgzt-ops"
PKG_CFG="$PKG_DIR/agent.yaml"

install -d -m 0755 "$INSTALL_DIR/bin" "$INSTALL_DIR/docs"
install -d -m 0755 "$CONF_DIR"

# 停掉旧版（linux-ops-agent）和新版服务，释放 19090。
systemctl disable --now linux-ops-agent >/dev/null 2>&1 || true
rm -f /etc/systemd/system/linux-ops-agent.service
systemctl disable --now "$SERVICE" >/dev/null 2>&1 || true
pkill -f "$INSTALL_DIR/bin/wxgzt-ops" >/dev/null 2>&1 || true

install -m 0755 "$BIN_SRC" "$INSTALL_DIR/bin/wxgzt-ops"
if [ -f "$PKG_DIR/agent-install.sh" ]; then
  install -m 0755 "$PKG_DIR/agent-install.sh" "$INSTALL_DIR/agent-install.sh"
fi
if [ -f "$PKG_DIR/README.md" ]; then
  install -m 0644 "$PKG_DIR/README.md" "$INSTALL_DIR/README.md"
fi

# 包内 update_base_url 是构建机上的占位值（127.0.0.1）。安装时若发现它是
# 空值或回环地址，就改成本次实际使用的制品桶，保证在线升级可用；其他配置
# 一律保持包内内容不变。
patch_update_url() {
  local f="$1" cur
  cur="$(grep -m1 '^[[:space:]]*update_base_url:' "$f" |
    sed 's/^[^:]*:[[:space:]]*//' | sed 's/[[:space:]]*#.*//' | tr -d "\"'")"
  case "$cur" in
    "" | *127.0.0.1* | *localhost*)
      sed -i "s|^\([[:space:]]*update_base_url:\).*|\1 \"${BASE_URL}\"|" "$f"
      log "包内 update_base_url 是占位值，已改为本次制品桶：$BASE_URL"
      ;;
  esac
}

if [ -n "$CONFIG_SRC" ]; then
  [ -f "$CONFIG_SRC" ] || die "配置文件不存在：$CONFIG_SRC"
  [ -r "$CONFIG_SRC" ] || die "配置文件不可读：$CONFIG_SRC"
  install -m 0644 "$CONFIG_SRC" "$CFG_FILE"
  log "已使用指定配置 $CONFIG_SRC -> $CFG_FILE"
  if [ -n "$ALLOW_SPEC" ] || [ -n "$LISTEN_ADDR" ]; then
    log "使用 --config，忽略 --allow-ip / --listen-addr（以配置文件为准）"
  fi
elif [ -f "$CFG_FILE" ] && [ "$FORCE" != "1" ]; then
  log "保留已有配置 $CFG_FILE（--force 用包内配置覆盖）"
  if [ -n "$ALLOW_SPEC" ] || [ -n "$LISTEN_ADDR" ]; then
    log "忽略 --allow-ip / --listen-addr（修改配置请加 --force 或 --config）"
  fi
elif [ -f "$LEGACY_CFG" ] && [ "$FORCE" != "1" ]; then
  install -m 0644 "$LEGACY_CFG" "$CFG_FILE"
  log "已迁移旧配置 $LEGACY_CFG -> $CFG_FILE"
elif [ -n "$ALLOW_SPEC" ] || [ -n "$LISTEN_ADDR" ]; then
  ALLOW_IPS=()
  ALLOW_YAML=""
  if [ -z "$LISTEN_ADDR" ]; then
    # auto 让本配置在任意主机上通用；Agent 启动时自行选择主用内网 IP。
    LISTEN_ADDR="auto"
  fi
  case "$LISTEN_ADDR" in
    0.0.0.0 | :: | "[::]" | "") die "listen-addr 不能是通配地址，请指定本机具体网卡 IP（如 10.0.0.11）或 auto" ;;
    auto | AUTO | Auto) LISTEN_ADDR="auto" ;;
    *[!0-9A-Fa-f:.]*) die "listen-addr 不是合法 IP：$LISTEN_ADDR" ;;
  esac
  # 展开逗号/空格分隔的 allow-ip，拒绝空值和通配地址。
  for entry in $(printf '%s' "$ALLOW_SPEC" | tr ',;' '  '); do
    [ -n "$entry" ] || continue
    case "$entry" in
      0.0.0.0 | :: | "0.0.0.0/0" | "::/0" | "*" | "") die "allow-ip 不允许通配地址：$entry" ;;
    esac
    case "$entry" in
      *[!0-9A-Fa-f:./]*) die "allow-ip 不是合法 IP/CIDR：$entry" ;;
    esac
    ALLOW_IPS+=("$entry")
  done
  [ "${#ALLOW_IPS[@]}" -gt 0 ] || die "必须至少指定一个 --allow-ip（允许访问 19090 的服务端 IP）"
  if [ "$LISTEN_ADDR" = "auto" ]; then
    log "listen_addr 设为 auto：Agent 启动时自动选择本机主用内网 IP"
  fi
  for entry in "${ALLOW_IPS[@]}"; do
    ALLOW_YAML="${ALLOW_YAML}  - \"${entry}\"
"
  done
  umask 077
  cat > "$CFG_FILE" <<EOF
# Generated by agent-install.sh. Rerun with --force to regenerate.
listen_addr: "${LISTEN_ADDR}"
listen_port: ${LISTEN_PORT}

# Only these source addresses may reach the agent (REST + WebSocket).
allowed_ips:
${ALLOW_YAML}
work_dir: "/tmp"
max_concurrent: 8
max_output_bytes: 10485760
max_script_bytes: 1048576
default_timeout_ms: 300000
max_timeout_ms: 3600000
task_retention_sec: 3600

terminal:
  shell: "/bin/bash"
  work_dir: "/"
  user: ""
  max_sessions: 4
  idle_timeout_sec: 600
  max_duration_sec: 3600
  history_mode: "memory"

update_base_url: "${BASE_URL}"
update_require_checksum: false
EOF
  chmod 0644 "$CFG_FILE"
  umask 022
  log "已生成配置 $CFG_FILE"
elif [ -f "$PKG_CFG" ]; then
  install -m 0644 "$PKG_CFG" "$CFG_FILE"
  patch_update_url "$CFG_FILE"
  log "已使用安装包内配置 -> $CFG_FILE"
else
  die "安装包内缺少 wxgzt-ops/agent.yaml；请用 --config 指定，或提供 --allow-ip 生成配置"
fi

# 自检用的监听地址：以实际生效配置文件为准（未写则用 Agent 内置默认值）。
PROBE_ADDR="$(sed -n 's/^[[:space:]]*listen_addr:[[:space:]]*\([^[:space:]#]*\).*$/\1/p' "$CFG_FILE" | head -n 1 | tr -d '"')"
PROBE_PORT="$(sed -n 's/^[[:space:]]*listen_port:[[:space:]]*\([0-9][0-9]*\).*$/\1/p' "$CFG_FILE" | head -n 1)"
PROBE_ADDR="${PROBE_ADDR:-${LISTEN_ADDR:-auto}}"
PROBE_PORT="${PROBE_PORT:-${LISTEN_PORT:-19090}}"
# listen_addr: auto 由 Agent 启动时自行探测；这里用同样的规则取本机地址做自检。
case "$PROBE_ADDR" in
  "" | auto | AUTO | Auto)
    PROBE_ADDR="$(detect_listen_addr)"
    [ -n "$PROBE_ADDR" ] || PROBE_ADDR="127.0.0.1"
    ;;
esac
case "$PROBE_ADDR" in
  127.0.0.1 | ::1)
    log "注意：$CFG_FILE 监听在 $PROBE_ADDR，只有本机能访问；跨机部署请改成 \"auto\" 或该主机的内网 IP"
    ;;
esac

UNIT="$TMP/wxgzt-ops.service"
if [ -f "$TMP/wxgzt-ops/wxgzt-ops.service" ]; then
  install -m 0644 "$TMP/wxgzt-ops/wxgzt-ops.service" "$UNIT"
else
  install -m 0644 "$(dirname "$0")/../deploy/systemd/wxgzt-ops.service" "$UNIT" 2>/dev/null ||
    die "包内缺少 wxgzt-ops.service"
fi
install -m 0644 "$UNIT" "/etc/systemd/system/$SERVICE.service"

systemctl daemon-reload
systemctl enable "$SERVICE" >/dev/null 2>&1 || true
if ! systemctl restart "$SERVICE"; then
  systemctl status "$SERVICE" --no-pager >&2 || true
  journalctl -u "$SERVICE" -n 20 --no-pager >&2 || true
  die "服务启动失败，请检查 $CFG_FILE（allowed_ips 必填且不能含通配地址）"
fi

# ---- 7. 自检（403 说明服务已启动、只是探测来源不在白名单）----
PROBE_HOST="$PROBE_ADDR"
case "$PROBE_ADDR" in
  *:*) PROBE_HOST="[$PROBE_ADDR]" ;;
esac
CODE=""
for _ in $(seq 1 20); do
  CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 \
    "http://${PROBE_HOST}:${PROBE_PORT}/api/v1/health" 2>/dev/null || true)"
  case "$CODE" in 200 | 403) break ;; esac
  sleep 0.5
done

if [ "$CODE" = "200" ] || [ "$CODE" = "403" ]; then
  printf '[wxgzt-ops] 安装完成：版本 %s，监听 %s:%s\n' "$VERSION" "$PROBE_ADDR" "$PROBE_PORT"
  if [ "${#ALLOW_IPS[@]}" -gt 0 ]; then
    printf '[wxgzt-ops] 允许来源：%s\n' "${ALLOW_IPS[*]}"
  fi
  printf '[wxgzt-ops] 配置：%s；服务：systemctl status %s\n' "$CFG_FILE" "$SERVICE"
  printf '[wxgzt-ops] 日志：journalctl -u %s -n 20 --no-pager\n' "$SERVICE"
  printf '[wxgzt-ops] 管理端新增主机时填 %s:%s；19090 只应对上述来源开放\n' "$PROBE_ADDR" "$PROBE_PORT"
else
  printf '[wxgzt-ops] 服务未就绪（HTTP %s）。排查：\n' "${CODE:-无响应}" >&2
  printf '  systemctl status %s\n  journalctl -u %s -n 50 --no-pager\n' "$SERVICE" "$SERVICE" >&2
  exit 1
fi
