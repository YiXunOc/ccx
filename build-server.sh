#!/usr/bin/env bash
# build-server.sh — CCX 服务端全量编译打包（不含桌面端）
#
# 每次运行均为全量构建：
#   1) 强制重建前端（忽略 hash 缓存）
#   2) 嵌入到 backend-go/frontend/dist
#   3) 交叉编译多平台服务端二进制到 dist/
#
# 用法:
#   ./build-server.sh                  # 默认: linux/amd64 + windows/amd64
#   ./build-server.sh all              # 全部目标
#   ./build-server.sh linux            # 仅 linux amd64+arm64
#   ./build-server.sh windows          # 仅 windows amd64+arm64
#   ./build-server.sh darwin           # 仅 darwin amd64+arm64
#   ./build-server.sh current          # 仅当前主机平台
#   ./build-server.sh windows/amd64    # 指定单个 GOOS/GOARCH
#   ./build-server.sh linux/amd64 windows/amd64
#
# 环境变量（可选）:
#   VERSION      覆盖版本号（默认读根目录 VERSION）
#   OUTPUT_DIR   产物目录（默认 <repo>/dist）
#   SKIP_FRONTEND=1  跳过前端重建（仅调试用，正式打包不要设）
#   CLEAN_DIST=0     不先清空本次目标对应旧产物（默认会删）

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FRONTEND_DIR="$ROOT_DIR/frontend"
BACKEND_DIR="$ROOT_DIR/backend-go"
EMBED_DIR="$BACKEND_DIR/frontend/dist"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/dist}"

GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
NC='\033[0m'

log()  { echo -e "${GREEN}$*${NC}"; }
warn() { echo -e "${YELLOW}$*${NC}"; }
err()  { echo -e "${RED}$*${NC}" >&2; }

die() {
  err "❌ $*"
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

# ---------- 工具链自举（不依赖调用方 shell 是否 source 过 PATH） ----------
bootstrap_toolchain() {
  # Go: 优先 PATH；否则探测常见安装位置
  if ! command -v go >/dev/null 2>&1; then
    local candidates=(
      "${GOROOT:+$GOROOT/bin/go}"
      "$HOME/sdk/go/bin/go"
      "$HOME/go/bin/go"
      "$HOME/.local/go/bin/go"
      "/usr/local/go/bin/go"
      "/usr/lib/go/bin/go"
    )
    local c
    for c in "${candidates[@]}"; do
      if [ -n "$c" ] && [ -x "$c" ]; then
        export GOROOT="$(cd "$(dirname "$c")/.." && pwd)"
        export PATH="$(dirname "$c"):${PATH:-}"
        break
      fi
    done
  fi

  # GOPATH / 已安装的 go tools (air 等)
  if [ -z "${GOPATH:-}" ]; then
    export GOPATH="${HOME}/go"
  fi
  if [ -d "${GOPATH}/bin" ]; then
    case ":${PATH}:" in
      *":${GOPATH}/bin:"*) ;;
      *) export PATH="${GOPATH}/bin:${PATH}" ;;
    esac
  fi

  # Bun: 优先 PATH；否则探测 ~/.bun/bin
  if ! command -v bun >/dev/null 2>&1; then
    local bun_candidates=(
      "$HOME/.bun/bin/bun"
      "$HOME/.local/bin/bun"
      "/usr/local/bin/bun"
    )
    local b
    for b in "${bun_candidates[@]}"; do
      if [ -x "$b" ]; then
        export PATH="$(dirname "$b"):${PATH:-}"
        break
      fi
    done
  fi

  # 国内网络默认 GOPROXY（调用方已设则不覆盖）
  if [ -z "${GOPROXY:-}" ]; then
    export GOPROXY="https://goproxy.cn,direct"
  fi
  if [ -z "${GOSUMDB:-}" ]; then
    export GOSUMDB="sum.golang.google.cn"
  fi
}

# ---------- 元数据 ----------
VERSION="${VERSION:-$(tr -d '[:space:]' < "$ROOT_DIR/VERSION" 2>/dev/null || echo "v0.0.0-dev")}"
BUILD_TIME="$(date -u '+%Y-%m-%d_%H:%M:%S')"
if git -C "$ROOT_DIR" rev-parse --short HEAD >/dev/null 2>&1; then
  GIT_COMMIT="$(git -C "$ROOT_DIR" rev-parse --short HEAD)"
else
  GIT_COMMIT="unknown"
fi

# 对齐 CI release.yml 的 ldflags
LDFLAGS="-s -w -linkmode=internal -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}"

# ---------- 目标解析 ----------
DEFAULT_TARGETS=("linux/amd64" "windows/amd64")

ALL_TARGETS=(
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
  "windows/arm64"
  "darwin/amd64"
  "darwin/arm64"
)

resolve_targets() {
  local args=("$@")
  local out=()
  local a t

  if [ "${#args[@]}" -eq 0 ]; then
    printf '%s\n' "${DEFAULT_TARGETS[@]}"
    return
  fi

  for a in "${args[@]}"; do
    case "$a" in
      all)
        printf '%s\n' "${ALL_TARGETS[@]}"
        return
        ;;
      current)
        out+=("$(go env GOOS)/$(go env GOARCH)")
        ;;
      linux)
        out+=("linux/amd64" "linux/arm64")
        ;;
      windows)
        out+=("windows/amd64" "windows/arm64")
        ;;
      darwin|macos)
        out+=("darwin/amd64" "darwin/arm64")
        ;;
      */*)
        out+=("$a")
        ;;
      *)
        die "未知目标: $a  (可用: all|current|linux|windows|darwin|GOOS/GOARCH)"
        ;;
    esac
  done

  # 去重保序
  local seen=""
  for t in "${out[@]}"; do
    case " $seen " in
      *" $t "*) ;;
      *)
        seen+=" $t"
        printf '%s\n' "$t"
        ;;
    esac
  done
}

binary_name() {
  local goos="$1" goarch="$2"
  local name="ccx-${goos}-${goarch}"
  if [ "$goos" = "windows" ]; then
    name="${name}.exe"
  fi
  printf '%s' "$name"
}

# ---------- 全量前端（强制，无缓存跳过） ----------
full_frontend_build() {
  log "📦 [1/3] 全量构建前端（强制重建，忽略 hash 缓存）..."
  if ! command -v bun >/dev/null 2>&1; then
    die "缺少命令: bun
  已探测: \$PATH、~/.bun/bin
  安装: curl -fsSL https://bun.sh/install | bash"
  fi
  require_cmd bun

  if [ ! -f "$FRONTEND_DIR/package.json" ]; then
    die "未找到前端工程: $FRONTEND_DIR"
  fi

  # 依赖缺失时自动安装（不碰桌面端）
  if [ ! -d "$FRONTEND_DIR/node_modules" ]; then
    warn "frontend/node_modules 不存在，执行 bun install..."
    (cd "$FRONTEND_DIR" && bun install)
  fi

  # 清掉旧产物与 embed 缓存，保证全量
  rm -rf "$FRONTEND_DIR/dist"
  rm -rf "$EMBED_DIR"
  rm -f "$ROOT_DIR/backend-go/frontend/dist/.build-sentinel" 2>/dev/null || true

  (cd "$FRONTEND_DIR" && bun run build)

  if [ ! -f "$FRONTEND_DIR/dist/index.html" ] || [ ! -d "$FRONTEND_DIR/dist/assets" ]; then
    die "前端构建产物不完整: $FRONTEND_DIR/dist"
  fi

  log "📋 嵌入前端到 Go 后端..."
  mkdir -p "$EMBED_DIR"
  cp -R "$FRONTEND_DIR/dist/." "$EMBED_DIR/"

  if [ ! -f "$EMBED_DIR/index.html" ] || [ ! -d "$EMBED_DIR/assets" ]; then
    die "前端嵌入产物不完整: $EMBED_DIR"
  fi

  # 写 hash 文件（供其他工具读取）；本脚本下次仍强制全量
  if command -v sha256sum >/dev/null 2>&1; then
    {
      find "$FRONTEND_DIR/src" "$FRONTEND_DIR/public" \
        "$FRONTEND_DIR/index.html" "$FRONTEND_DIR/vite.config.ts" \
        "$FRONTEND_DIR/tsconfig.json" "$FRONTEND_DIR/tsconfig.app.json" \
        "$FRONTEND_DIR/bun.lock" \
        -type f 2>/dev/null \
        | sort \
        | while IFS= read -r p; do sha256sum "$p"; done
    } | sha256sum | cut -d' ' -f1 > "$EMBED_DIR/.source-hash" || true
  fi

  log "✅ 前端全量构建并嵌入完成"
}

# ---------- Go 服务端交叉编译 ----------
build_one() {
  local target="$1"
  local goos goarch out
  goos="${target%/*}"
  goarch="${target#*/}"
  out="$OUTPUT_DIR/$(binary_name "$goos" "$goarch")"

  log "🔧 编译 ${goos}/${goarch} → ${out}"

  (
    cd "$BACKEND_DIR"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build \
        -trimpath \
        -buildvcs=false \
        -ldflags "$LDFLAGS" \
        -o "$out" \
        .
  )

  if [ ! -f "$out" ]; then
    die "编译失败，未生成: $out"
  fi

  if command -v file >/dev/null 2>&1; then
    warn "   $(file -b "$out")"
  fi
  warn "   size: $(du -h "$out" | awk '{print $1}')"
}

write_checksums() {
  # 参数: 本次构建的目标列表（GOOS/GOARCH）
  local targets=("$@")
  local names=()
  local t goos goarch n

  log "🔏 生成 checksums（仅本次目标）..."
  for t in "${targets[@]}"; do
    goos="${t%/*}"
    goarch="${t#*/}"
    n="$(binary_name "$goos" "$goarch")"
    if [ -f "$OUTPUT_DIR/$n" ]; then
      names+=("$n")
    fi
  done

  if [ "${#names[@]}" -eq 0 ]; then
    warn "无产物可校验"
    return 0
  fi

  (
    cd "$OUTPUT_DIR"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum "${names[@]}" | sort -k2 > checksums-server.txt
    elif command -v shasum >/dev/null 2>&1; then
      shasum -a 256 "${names[@]}" | sort -k2 > checksums-server.txt
    else
      warn "无 sha256sum/shasum，跳过 checksums"
      return 0
    fi
  )
  if [ -f "$OUTPUT_DIR/checksums-server.txt" ]; then
    log "✅ $OUTPUT_DIR/checksums-server.txt"
    cat "$OUTPUT_DIR/checksums-server.txt"
  fi
}

# ---------- main ----------
main() {
  # help 必须在主进程处理：process substitution 里 exit 不会结束主脚本
  case "${1:-}" in
    -h|--help|help)
      sed -n '1,40p' "$0"
      exit 0
      ;;
  esac

  bootstrap_toolchain

  if ! command -v go >/dev/null 2>&1; then
    die "缺少命令: go
  已探测: \$PATH、\$GOROOT、~/sdk/go、~/go、~/.local/go、/usr/local/go
  本机若已装 Go，请先:
    export GOROOT=\$HOME/sdk/go
    export PATH=\$GOROOT/bin:\$PATH
  或安装 Go ≥1.25 后重试。"
  fi
  require_cmd go

  mapfile -t TARGETS < <(resolve_targets "$@")
  if [ "${#TARGETS[@]}" -eq 0 ]; then
    die "没有可构建的目标"
  fi

  log "=========================================="
  log " CCX 服务端全量打包（不含桌面端）"
  log "=========================================="
  log " root:      $ROOT_DIR"
  log " version:   $VERSION"
  log " build:     $BUILD_TIME UTC"
  log " commit:    $GIT_COMMIT"
  log " output:    $OUTPUT_DIR"
  log " targets:   ${TARGETS[*]}"
  log "=========================================="

  if [ ! -f "$BACKEND_DIR/go.mod" ]; then
    die "未找到后端工程: $BACKEND_DIR"
  fi

  # 1) 全量前端
  if [ "${SKIP_FRONTEND:-0}" = "1" ]; then
    warn "⚠ SKIP_FRONTEND=1：跳过前端重建（调试模式）"
    if [ ! -f "$EMBED_DIR/index.html" ]; then
      die "SKIP_FRONTEND=1 但嵌入前端不存在: $EMBED_DIR"
    fi
  else
    full_frontend_build
  fi

  # 2) 准备产物目录：清掉本次目标对应旧文件
  mkdir -p "$OUTPUT_DIR"
  if [ "${CLEAN_DIST:-1}" != "0" ]; then
    local t goos goarch old
    for t in "${TARGETS[@]}"; do
      goos="${t%/*}"
      goarch="${t#*/}"
      old="$OUTPUT_DIR/$(binary_name "$goos" "$goarch")"
      rm -f "$old"
    done
  fi

  # 3) 编译所有目标
  log "📦 [2/3] 编译服务端二进制..."
  local t
  for t in "${TARGETS[@]}"; do
    build_one "$t"
  done

  # 4) checksums
  log "📦 [3/3] 汇总..."
  write_checksums "${TARGETS[@]}"

  echo
  log "✅ 全量打包完成"
  log "产物目录: $OUTPUT_DIR"
  ls -lh "$OUTPUT_DIR"/ccx-* 2>/dev/null || true
  echo
  warn "说明:"
  warn "  - 仅服务端（console），不含 Wails 桌面 / NSIS / MSIX"
  warn "  - Windows 产物无 Authenticode 签名（签名走 GitHub Release CI）"
  warn "  - 运行前请配置环境变量或 .env（参考 backend-go/.env.example）"
}

main "$@"
