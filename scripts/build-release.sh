#!/usr/bin/env bash
# Build production release packages for wxgzt-ops (backend + agent + frontend).
#
#   scripts/build-release.sh                     # linux amd64 + arm64, obfuscated
#   scripts/build-release.sh --archs amd64       # single arch
#   scripts/build-release.sh --with-frontend     # also build/hash the SPA
#   scripts/build-release.sh --frontend-only     # only build/package the SPA
#   scripts/build-release.sh --no-obfuscate      # debug build (not for prod)
#
# Obfuscation: only the agent is built with garble (-literals). The backend is
# deliberately built without garble: GORM derives table and column names from Go
# type/field names through reflection, and garble renames those identifiers, so
# an obfuscated backend queries names like rd_yvyos instead of servers and no
# longer matches database/schema.sql. Both binaries still use -trimpath -s -w.
# Install garble first:
#
#   go install mvdan.cc/garble@latest
#
# Layout:
#
#   bin/release/                            uploaded to the MinIO agent bucket
#     wxgzt-ops-agent-<ver>-linux-<arch>.tar.gz
#     SHA256SUMS                            build record only: bin/minio_upload.py
#                                           embeds these checksums into the
#                                           uploaded installer, so the bucket
#                                           holds just the tarballs + script
#
#   bin/release/server/                     management-side deployment files
#     wxgzt-ops-backend-<ver>-linux-<arch>  backend binary
#     wxgzt-ops-server-<ver>-linux-<arch>.tar.gz
#                                           backend + frontend + configs +
#                                           systemd/nginx + database/schema.sql
#     wxgzt-ops-frontend-<ver>.tar.gz       built SPA only (--with-frontend)
#     SHA256SUMS
#
# Publish with bin/minio_upload.py: it uploads the tarball and a rendered
# agent-install.sh (bucket URL + SHA256SUMS baked in). Nothing under server/
# belongs in the agent bucket.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/bin/release"
VERSION="${VERSION:-$(sed -n 's/^var version = "\(.*\)"$/\1/p' "$ROOT/agent/main.go")}"
VERSION="${VERSION:-0.0.0}"
ARCHS="${ARCHS:-amd64 arm64}"
OBFUSCATE=1
WITH_FRONTEND=0
FRONTEND_ONLY=0
WITH_UPX=0

while [ $# -gt 0 ]; do
  case "$1" in
    --archs) ARCHS="${2:-}"; shift 2 ;;
    --archs=*) ARCHS="${1#*=}"; shift ;;
    --version) VERSION="${2:-}"; shift 2 ;;
    --version=*) VERSION="${1#*=}"; shift ;;
    --with-frontend) WITH_FRONTEND=1; shift ;;
    --frontend-only) WITH_FRONTEND=1; FRONTEND_ONLY=1; shift ;;
    --no-obfuscate) OBFUSCATE=0; shift ;;
    --upx) WITH_UPX=1; shift ;;
    -h | --help) sed -n '2,31p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [ "$OBFUSCATE" = "1" ] && [ "$FRONTEND_ONLY" = "0" ] &&
  ! command -v garble >/dev/null 2>&1; then
  echo "garble not found. Install it with: go install mvdan.cc/garble@latest" >&2
  echo "or pass --no-obfuscate for a debug build (not recommended for production)." >&2
  exit 1
fi
if [ "$WITH_UPX" = "1" ] && ! command -v upx >/dev/null 2>&1; then
  echo "--upx requested but upx is not installed" >&2
  exit 1
fi

mkdir -p "$OUT/server"
# Remove artifacts from the pre-rename flat layout; those files are regenerated
# with the wxgzt-ops-agent-* / server/ naming below.
rm -f "$OUT"/wxgzt-ops-[0-9]*.tar.gz "$OUT"/wxgzt-ops-backend-*-linux-*
STAGE="$OUT/.stage"
rm -rf "$STAGE"
trap 'rm -rf "$STAGE"' EXIT

# build GOOS/GOARCH binary; obfuscated when available
build_bin() { # <source dir> <output> <arch> <ldflags>
  local dir="$1" out="$2" arch="$3" ldflags="$4"
  if [ "$OBFUSCATE" = "1" ]; then
    ( cd "$dir" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
        garble -literals build -trimpath -buildvcs=false \
        -ldflags "$ldflags" -o "$out" . )
  else
    ( cd "$dir" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
        go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$out" . )
  fi
  if [ "$WITH_UPX" = "1" ]; then
    upx --best --quiet "$out" >/dev/null
  fi
}

# GORM reflection depends on Go identifier names (see the header), so the
# backend is never garbled, even when OBFUSCATE=1 for the agent.
build_backend_bin() { # <output> <arch> <ldflags>
  local out="$1" arch="$2" ldflags="$3"
  ( cd "$ROOT/backend" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
      go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$out" . )
  if [ "$WITH_UPX" = "1" ]; then
    upx --best --quiet "$out" >/dev/null
  fi
}

# Git Bash on Windows loses Unix mode bits (chmod is a no-op on NTFS), so the
# executable bits must be rewritten into the archive after tarring.
FIX_MODES=0
case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*) FIX_MODES=1 ;;
esac
fix_tar_mode() { # <tar.gz> <octal mode> <member>...
  local archive="$1" mode="$2"
  shift 2
  local py=""
  # Windows ships a python3 stub that exits 49 without running anything, so
  # probe that the interpreter actually works before choosing it.
  if command -v python >/dev/null 2>&1 && python -c 'import sys' >/dev/null 2>&1; then
    py=python
  elif command -v python3 >/dev/null 2>&1 && python3 -c 'import sys' >/dev/null 2>&1; then
    py=python3
  else
    echo "warning: python not found; run chmod +x on extracted binaries" >&2
    return 0
  fi
  "$py" - "$archive" "$mode" "$@" <<'PY'
import os, sys, tarfile

archive, mode = sys.argv[1], int(sys.argv[2], 8)
names = set(sys.argv[3:])
tmp = archive + ".tmp"
with tarfile.open(archive, "r:gz") as src, tarfile.open(tmp, "w:gz") as dst:
    for member in src:
        if member.name in names:
            member.mode = mode
        data = src.extractfile(member) if member.isfile() else None
        dst.addfile(member, data)
os.replace(tmp, archive)
PY
}

echo "==> wxgzt-ops release $VERSION (obfuscate=$OBFUSCATE, archs=$ARCHS)"
if [ "$FRONTEND_ONLY" = "0" ]; then
for ARCH in $ARCHS; do
  echo "==> linux/$ARCH"
  AGENT_BIN="$STAGE/agent-$ARCH"
  build_bin "$ROOT/agent" "$AGENT_BIN" "$ARCH" \
    "-s -w -X main.version=$VERSION"
  build_backend_bin "$OUT/server/wxgzt-ops-backend-$VERSION-linux-$ARCH" "$ARCH" "-s -w"

  PKG_ROOT="$STAGE/pkg-$ARCH/wxgzt-ops"
  mkdir -p "$PKG_ROOT"
  cp "$AGENT_BIN" "$PKG_ROOT/wxgzt-ops"
  chmod 0750 "$PKG_ROOT/wxgzt-ops"
  cp "$ROOT/configs/agent.yaml" "$PKG_ROOT/agent.yaml"
  cp "$ROOT/deploy/systemd/wxgzt-ops.service" "$PKG_ROOT/wxgzt-ops.service"
  # Ship the installer inside the agent package too, so an extracted tarball
  # can reinstall/upgrade on the host without fetching the script separately.
  cp "$ROOT/scripts/agent-install.sh" "$PKG_ROOT/agent-install.sh"
  chmod 0755 "$PKG_ROOT/agent-install.sh"
  cat > "$PKG_ROOT/README.md" <<EOF
# wxgzt-ops agent $VERSION (linux/$ARCH)

Install (no arguments needed; the bucket URL and package checksum are baked
into the published script):

    curl -fsSL https://<minio>/<bucket>/agent-install.sh | sudo bash

The installer extracts this package into /opt/wxgzt-ops and uses the included
agent.yaml as /opt/wxgzt-ops/agent.yaml (edit allowed_ips before building for
your backend IPs; listen_addr: "auto" works unchanged on every host). An
existing /opt/wxgzt-ops/agent.yaml is preserved on reinstall; --force replaces
it with the packaged one. update_base_url is pointed at the real bucket at
install time when the packaged value is a loopback placeholder.

The same installer is included here as agent-install.sh for offline use. It
has no baked-in checksums, so either keep a SHA256SUMS object in the bucket
or set AGENT_ALLOW_NO_CHECKSUM=1 after verifying the tarball yourself:

    sudo bash agent-install.sh --base-url https://<minio>/<bucket> \\
      --config /opt/wxgzt-ops/agent.yaml

Installed layout: /opt/wxgzt-ops/bin/wxgzt-ops, config
/opt/wxgzt-ops/agent.yaml, systemd unit wxgzt-ops.
The agent requires an explicit allowed_ips list and refuses to start on
0.0.0.0 or a wildcard allowlist.
EOF

  tar -czf "$OUT/wxgzt-ops-agent-$VERSION-linux-$ARCH.tar.gz" \
    -C "$STAGE/pkg-$ARCH" wxgzt-ops
  if [ "$FIX_MODES" = "1" ]; then
    fix_tar_mode "$OUT/wxgzt-ops-agent-$VERSION-linux-$ARCH.tar.gz" 0755 \
      "wxgzt-ops/wxgzt-ops" "wxgzt-ops/agent-install.sh"
  fi
done
fi

if [ "$WITH_FRONTEND" = "1" ]; then
  echo "==> frontend"
  FE_BASE="${VITE_BASE_PATH:-/ops-wxgzt/}"
  FE_API="${VITE_API_BASE:-/ops-server/api/v1}"
  (
    cd "$ROOT/frontend"
    [ -d node_modules ] || npm ci
    # MSYS2_ENV_CONV_EXCL: Git Bash on Windows would rewrite /ops-... paths.
    MSYS2_ENV_CONV_EXCL='VITE_BASE_PATH;VITE_API_BASE' \
      VITE_BASE_PATH="$FE_BASE" VITE_API_BASE="$FE_API" npm run build
  )
  DIST="$ROOT/frontend/dist"
  if [ "$FE_BASE" = "/" ]; then
    FE_DIR="."
  else
    FE_DIR="$(printf '%s' "$FE_BASE" | sed 's#^/##; s#/$##')"
  fi
  [ -f "$DIST/$FE_DIR/index.html" ] ||
    { echo "frontend build output not found: $DIST/$FE_DIR/index.html" >&2; exit 1; }

  mkdir -p "$OUT/server"
  tar -czf "$OUT/server/wxgzt-ops-frontend-$VERSION.tar.gz" -C "$DIST" "$FE_DIR"

  if [ "$FRONTEND_ONLY" = "0" ]; then
  for ARCH in $ARCHS; do
    echo "==> server bundle linux/$ARCH"
    SB="$STAGE/server-$ARCH/wxgzt-ops-server-$VERSION"
    mkdir -p "$SB"/{backend,frontend,configs,database,systemd,nginx,docs}
    install -m 0755 "$OUT/server/wxgzt-ops-backend-$VERSION-linux-$ARCH" \
      "$SB/backend/wxgzt-ops-backend"
    cp -a "$DIST/." "$SB/frontend/"
    cp "$ROOT/configs/backend.yaml" "$SB/configs/"
    cp "$ROOT/database/schema.sql" "$SB/database/"
    cp "$ROOT/deploy/systemd/wxgzt-ops-backend.service" "$SB/systemd/"
    cp "$ROOT/deploy/systemd/wxgzt-ops.service" "$SB/systemd/"
    cp "$ROOT/deploy/nginx.conf.example" "$ROOT/deploy/nginx-prefix.conf.example" "$SB/nginx/"
    cp "$ROOT/docs/production-deploy.md" "$SB/docs/"
    cat > "$SB/README.md" <<EOF
# wxgzt-ops server bundle $VERSION (linux/$ARCH)

Layout:
  backend/wxgzt-ops-backend   management backend binary
  frontend/                   built SPA (mount path: $FE_BASE)
  configs/backend.yaml        config template; secrets go in backend.env
  database/schema.sql         MySQL 8 initialization (default DB: linux_ops;
                              replace the name with sed for wxgzt_ops)
  systemd/                    wxgzt-ops-backend.service, wxgzt-ops.service
  nginx/                      reverse-proxy examples
  docs/production-deploy.md   full deployment guide

The agent installer (agent-install.sh) is NOT in this bundle: it is published
as a separate object at the root of the MinIO agent bucket.

Suggested order:
  1. mysql < database/schema.sql
  2. install backend/wxgzt-ops-backend -> /opt/wxgzt-ops/bin/backend
  3. copy frontend/* -> /opt/wxgzt-ops/frontend/
  4. write /etc/wxgzt-ops/backend.env (DB_*, JWT_SECRET, MINIO_*)
  5. systemctl enable --now wxgzt-ops-backend
  6. deploy nginx config and reload

See docs/production-deploy.md for details.
EOF
    tar -czf "$OUT/server/wxgzt-ops-server-$VERSION-linux-$ARCH.tar.gz" \
      -C "$STAGE/server-$ARCH" "wxgzt-ops-server-$VERSION"
    if [ "$FIX_MODES" = "1" ]; then
      fix_tar_mode "$OUT/server/wxgzt-ops-server-$VERSION-linux-$ARCH.tar.gz" 0755 \
        "wxgzt-ops-server-$VERSION/backend/wxgzt-ops-backend"
    fi
  done
  fi

fi

echo "==> checksums"
if [ "$FRONTEND_ONLY" = "0" ]; then
(
  cd "$OUT"
  rm -f SHA256SUMS
  for f in wxgzt-ops-agent-"$VERSION"-linux-*.tar.gz; do
    [ -f "$f" ] && sha256sum "$f" >> SHA256SUMS
  done
)
fi
(
  cd "$OUT/server"
  rm -f SHA256SUMS
  for f in wxgzt-ops-backend-* wxgzt-ops-frontend-* wxgzt-ops-server-*; do
    [ -f "$f" ] && sha256sum "$f" >> SHA256SUMS
  done
)

echo
if [ "$FRONTEND_ONLY" = "1" ]; then
  echo "Frontend package in $OUT/server:"
  ls -lh "$OUT/server/wxgzt-ops-frontend-$VERSION.tar.gz"
  echo "(mount path: $FE_BASE; extract to /opt/wxgzt-ops/frontend/ or an nginx root)"
  exit 0
fi
echo "Agent artifacts in $OUT (upload these to the MinIO bucket):"
ls -lh "$OUT" | sed '1d'
echo
echo "Server-side artifacts in $OUT/server (do NOT upload to the agent bucket):"
ls -lh "$OUT/server" | sed '1d'
echo
echo "Publish with: python3 bin/minio_upload.py --file wxgzt-ops-agent-$VERSION-linux-amd64.tar.gz"
echo "It uploads the tarball and a rendered agent-install.sh (bucket URL + SHA256"
echo "baked in), so the bucket holds only the packages and the installer."
