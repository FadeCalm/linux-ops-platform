#!/usr/bin/env bash
# 一键下载并部署 Linux Ops Platform Agent（linux/amd64，以 root 运行）。
#
# 用法（建议先下载再执行，不要 curl | bash）：
#   curl -fsSL http://47.114.120.205:9000/linux-ops-agent/agent-install.sh -o /tmp/agent-install.sh
#   sudo bash /tmp/agent-install.sh [监听地址]
#
# 监听地址默认 0.0.0.0（便于远端后端访问）。若只需本机访问可传 127.0.0.1；
# 若后端走内网，可传该主机内网 IP。
#
# 环境变量：
#   AGENT_BASE_URL  包下载地址前缀，默认 http://47.114.120.205:9000/linux-ops-agent
#   AGENT_VERSION   指定版本（默认读取制品库的 manifest.json，即最新版）
set -euo pipefail

BASE_URL="${AGENT_BASE_URL:-http://47.114.120.205:9000/linux-ops-agent}"
LISTEN_ADDR="${1:-0.0.0.0}"

if [ "$(id -u)" -ne 0 ]; then
  echo "错误：需要 root 权限，请用 sudo bash $0" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64|amd64) ;;
  *)
    echo "错误：当前包只支持 x86_64，检测到 $(uname -m)" >&2
    exit 1
    ;;
esac

for cmd in curl tar sha256sum; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "错误：缺少命令 $cmd" >&2; exit 1; }
done

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# 解析版本：默认跟随 manifest.json，或由 AGENT_VERSION 显式指定。
USE_SUMS=0
if [ -n "${AGENT_VERSION:-}" ]; then
  VERSION="$AGENT_VERSION"
  PKG="linux-ops-agent-${VERSION}-linux-amd64.tar.gz"
  USE_SUMS=1
else
  echo "[1/4] 读取 $BASE_URL/manifest.json"
  curl -fsSL --retry 3 --max-time 60 "$BASE_URL/manifest.json" -o "$TMP/manifest.json"
  manifest_field() {
    sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$TMP/manifest.json" | head -n 1
  }
  VERSION="$(manifest_field version)"
  PKG="$(manifest_field file)"
  SHA256="$(manifest_field sha256)"
  if [ -z "$VERSION" ] || [ -z "$PKG" ] || [ -z "$SHA256" ]; then
    echo "错误：manifest.json 内容不完整" >&2
    exit 1
  fi
fi

echo "[1/4] 下载 $BASE_URL/$PKG"
curl -fsSL --retry 3 --max-time 300 "$BASE_URL/$PKG" -o "$TMP/$PKG"
if [ "$USE_SUMS" = 1 ]; then
  curl -fsSL --retry 3 --max-time 60 "$BASE_URL/SHA256SUMS" -o "$TMP/SHA256SUMS"
fi

echo "[2/4] 校验 SHA256"
if [ "$USE_SUMS" = 1 ]; then
  ( cd "$TMP" && grep -F "$PKG" SHA256SUMS | sha256sum -c - )
else
  ( cd "$TMP" && printf '%s  %s\n' "$SHA256" "$PKG" | sha256sum -c - )
fi

echo "[3/4] 安装文件"
tar xzf "$TMP/$PKG" -C "$TMP"
install -d -m 0755 /opt/linux-ops-platform/bin /etc/linux-ops
install -m 0755 "$TMP/linux-ops-agent/agent" /opt/linux-ops-platform/bin/agent

if [ ! -f /etc/linux-ops/agent.yaml ]; then
  install -m 0644 "$TMP/linux-ops-agent/agent.yaml" /etc/linux-ops/agent.yaml
  sed -i "s/^listen_addr:.*/listen_addr: \"$LISTEN_ADDR\"/" /etc/linux-ops/agent.yaml
  echo "      已生成 /etc/linux-ops/agent.yaml（listen_addr=$LISTEN_ADDR）"
else
  echo "      保留已有配置 /etc/linux-ops/agent.yaml"
fi

echo "[4/4] 以 root 启动 Agent"
pkill -f '/opt/linux-ops-platform/bin/agent' >/dev/null 2>&1 || true
sleep 0.5
nohup /opt/linux-ops-platform/bin/agent -config /etc/linux-ops/agent.yaml \
  >> /var/log/linux-ops-agent.log 2>&1 &

PROBE="$(awk -F'"' '/^listen_addr:/{print $2; exit}' /etc/linux-ops/agent.yaml)"
case "$PROBE" in
  ""|0.0.0.0) PROBE=127.0.0.1 ;;
esac

ok=0
for _ in $(seq 1 20); do
  if curl -fsS --max-time 2 "http://$PROBE:19090/api/v1/health" >/dev/null 2>&1; then
    ok=1
    break
  fi
  sleep 0.5
done

echo
if [ "$ok" -eq 1 ]; then
  curl -fsS --max-time 3 "http://$PROBE:19090/api/v1/info" || true
  echo
  echo "Agent 已启动并响应。"
else
  echo "警告：Agent 未在 5 秒内响应，请查看日志："
  echo "  tail -n 100 /var/log/linux-ops-agent.log"
fi
echo "  版本: $VERSION"
echo "  配置: /etc/linux-ops/agent.yaml"
echo "  日志: /var/log/linux-ops-agent.log"
echo "  端口: 19090/tcp（只放行后端所在主机的 IP）"
echo "  平台: 在「服务器管理」新增主机，IP 填本机对后端可达的地址，端口 19090"
echo
echo "安全提醒：Agent 无认证且以 root 运行，切勿把 19090 暴露给不可信来源。"
