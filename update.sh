#!/usr/bin/env bash
# Ghoul 一键更新脚本：拉取最新代码后，默认在本地重建 Docker 镜像并重启服务。
#
# 用法：
#   ./update.sh                 拉取代码 -> 本地构建 Docker 镜像 -> 重启 ghoul
#   ./update.sh --no-pull       跳过 git pull，使用当前代码构建
#   ./update.sh --no-build      跳过镜像构建，只重启现有镜像
#   ./update.sh --local         本地编译 Go 二进制，不更新 Docker
#   ./update.sh --tag v0.3.3    构建并标记为 ghoul:v0.3.3
#
# 数据库 schema 由 ghoul 启动时自动迁移；pgdata、data、skills 都是绑定挂载，
# 更新脚本不会删除这些持久化数据。
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

env_file_get(){
  local key="$1"
  [ -f .env ] || return 0
  awk -F= -v key="$key" '$1 == key { v = substr($0, length(key) + 2) } END { if (v != "") print v }' .env | tr -d '\r'
}

set_env_value(){
  local key="$1" value="$2" tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/ghoul-env.XXXXXX")"
  awk -v key="$key" -v value="$value" '
    BEGIN { found = 0 }
    index($0, key "=") == 1 { print key "=" value; found = 1; next }
    { print }
    END { if (!found) print key "=" value }
  ' .env >"$tmp"
  mv "$tmp" .env
}

usage(){
  cat <<'EOF'
用法：
  ./update.sh [选项]

选项：
  --docker       更新 Docker 部署（默认）
  --local        本地编译 Go 二进制
  --no-pull      跳过 git pull --ff-only
  --no-build     跳过 docker compose build，只重启服务
  --tag TAG      设置 GHOUL_TAG，构建为 ghoul:TAG
  -h, --help     显示帮助
EOF
}

sync_repo(){
  if ! command -v git >/dev/null 2>&1; then
    warn "未检测到 git，跳过代码拉取，使用当前文件"
    return
  fi
  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    warn "当前目录不是 git 仓库，跳过代码拉取"
    return
  fi

  local old_commit new_commit
  old_commit="$(git rev-parse --short HEAD 2>/dev/null || true)"
  info "拉取最新代码：git pull --ff-only"
  if ! git pull --ff-only; then
    die "git pull 失败。请先处理本地改动或分支冲突；只想用当前代码构建时执行 ./update.sh --no-pull"
  fi
  new_commit="$(git rev-parse --short HEAD 2>/dev/null || true)"
  ok "代码已更新：${old_commit:-unknown} -> ${new_commit:-unknown}"
}

update_docker(){
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 \
    || die "未检测到 docker / docker compose，请先完成 Docker 安装"
  [ -f .env ] || die "未找到 .env，请先执行 ./install.sh 完成首次部署"

  local image_repo image_tag
  if [ -n "${TAG:-}" ]; then
    case "$TAG" in
      *[!A-Za-z0-9._-]*) die "无效镜像 tag：$TAG" ;;
    esac
    set_env_value GHOUL_TAG "$TAG"
    ok "已设置 GHOUL_TAG=$TAG"
  fi

  image_repo="$(env_file_get GHOUL_IMAGE)"
  image_repo="${image_repo:-ghoul}"
  image_tag="$(env_file_get GHOUL_TAG)"
  [ -n "$image_tag" ] || image_tag="$(env_file_get ARTEX_TAG)"
  image_tag="${image_tag:-local}"

  if [ "${DO_BUILD}" -eq 1 ]; then
    info "本地构建镜像：${image_repo}:${image_tag}"
    docker compose build ghoul
  else
    warn "跳过镜像构建，直接使用现有 ${image_repo}:${image_tag}"
  fi

  info "重启 ghoul（启动时自动迁移 schema）"
  docker compose up -d ghoul
  ok "更新完成 -> http://localhost:8787"
  info "查看日志：docker compose logs -f ghoul"
}

update_local(){
  command -v go >/dev/null 2>&1 || die "未检测到 Go（>=1.26）：https://go.dev/dl/"
  [ -f config.json ] || warn "未找到 config.json，请先执行 ./install.sh 完成首次配置"
  ok "Go: $(go version)"

  if command -v npm >/dev/null 2>&1; then
    info "重建前端静态产物"
    (cd web && npm ci && npm run build:static)
    rm -rf server/webui/dist
    cp -r web/out server/webui/dist
    info "编译内嵌前端的 Go 二进制"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o ghoul ./cmd/ghoul
  else
    warn "未检测到 npm，将编译不含内嵌前端的二进制"
    CGO_ENABLED=0 go build -o ghoul ./cmd/ghoul
  fi

  ok "编译完成：./ghoul"
  warn "通过 ./start.sh 重启进程后生效；容器内进程不会被本地编译覆盖"
}

MODE=docker
DO_PULL=1
DO_BUILD=1
TAG=

while [ "$#" -gt 0 ]; do
  case "$1" in
    --docker) MODE=docker; shift ;;
    --local) MODE=local; shift ;;
    --no-pull) DO_PULL=0; shift ;;
    --no-build) DO_BUILD=0; shift ;;
    --tag)
      [ "$#" -ge 2 ] || die "--tag 需要镜像标签"
      TAG="$2"
      shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数：$1（使用 --help 查看用法）" ;;
  esac
done

[ "$MODE" = docker ] || [ "$DO_BUILD" -eq 1 ] || die "--no-build 仅适用于 Docker 更新"
[ "$MODE" = docker ] || [ -z "$TAG" ] || die "--tag 仅适用于 Docker 更新"

echo "=============================="
echo "  GHOUL 更新"
echo "  模式：$MODE"
echo "=============================="

if [ "$DO_PULL" -eq 1 ]; then
  sync_repo
else
  warn "已跳过代码拉取，使用当前工作区代码"
fi

case "$MODE" in
  docker) update_docker ;;
  local) update_local ;;
  *) die "无效更新模式：$MODE" ;;
esac
