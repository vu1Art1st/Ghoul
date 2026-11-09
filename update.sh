#!/usr/bin/env bash
# Ghoul 更新脚本：① Docker 更新（拉新镜像重建）  ② 本地编译更新（重建二进制）
# 与 install.sh 对应：install 负责首次落地，update 负责升级到新版本。
# DB 迁移无需手动执行——ghoul 每次启动都会幂等重跑 schema.sql（含 ADD COLUMN/CREATE
# INDEX IF NOT EXISTS），所以“重启即迁移”。数据（pgdata 卷、./data、./skills）不受影响。
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
env_file_get(){
  local key="$1"
  [ -f .env ] || return 0
  awk -F= -v key="$key" '$1 == key { v = substr($0, length(key) + 2) } END { if (v != "") print v }' .env
}

# ── 可选：同步仓库到最新代码（compose/脚本/本地编译源码都靠它更新）───────
sync_repo(){
  [ -d .git ] && command -v git >/dev/null 2>&1 || { warn "非 git 工作副本，跳过 git pull"; return; }
  [ "$(ask '拉取最新代码 (git pull --ff-only)? (y/n)' y)" = y ] || return
  if ! git pull --ff-only; then
    warn "git pull 未能快进（本地有改动或分支分叉）——请手动处理后重试，本次沿用当前代码"
  fi
}

# ── ① Docker 更新 ───────────────────────────────
update_docker(){
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 \
    || die "未检测到 docker / docker compose，请先用 ./install.sh 安装部署"
  [ -f .env ] || die "未找到 .env，请先运行 ./install.sh 完成首次部署"

  # 可选：升级到指定版本 tag（GHOUL_TAG 优先，旧 ARTEX_TAG 继续兼容）
  local current_tag tag
  current_tag="$(env_file_get GHOUL_TAG)"
  [ -n "$current_tag" ] || current_tag="$(env_file_get ARTEX_TAG)"
  tag="$(ask "目标镜像 tag（回车沿用 ${current_tag:-latest}）" '')"
  if [ -n "$tag" ]; then
    if grep -q '^GHOUL_TAG=' .env; then
      sed -i.bak "s|^GHOUL_TAG=.*|GHOUL_TAG=${tag}|" .env && rm -f .env.bak
    elif grep -q '^ARTEX_TAG=' .env; then
      sed -i.bak "s|^ARTEX_TAG=.*|GHOUL_TAG=${tag}|" .env && rm -f .env.bak
    else
      printf '\nGHOUL_TAG=%s\n' "$tag" >> .env
    fi
    ok "已将 GHOUL_TAG 设为 ${tag}"
  fi

  # 只动 ghoul：postgres 是固定的 16-alpine，不需要跟着升级（拉它纯属浪费带宽，
  # 且大版本变动还会有兼容风险）。ghoul 声明了 depends_on postgres，所以带服务名
  # up 时若 pg 没起会自动拉起，已在跑的则原样保留、不重建。
  info "拉取新镜像（仅 ghoul）…"
  if ! docker compose pull ghoul; then
    warn "镜像拉取失败，尝试从源码构建 ghoul 镜像"
    docker compose build ghoul
  fi
  info "重建并启动（ghoul 重启时自动迁移 schema）…"
  docker compose up -d ghoul
  ok "更新完成 → http://localhost:8787"
  info "查看日志：docker compose logs -f ghoul"
  info "清理旧镜像（可选）：docker image prune -f"
}

# ── ② 本地编译更新 ──────────────────────────────
update_local(){
  command -v go >/dev/null 2>&1 || die "未检测到 Go（>=1.26）：https://go.dev/dl/"
  [ -f config.json ] || warn "未找到 config.json——若首次部署请改用 ./install.sh"
  ok "Go: $(go version)"

  if command -v npm >/dev/null 2>&1; then
    info "重建前端静态产物…"
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "重新编译内嵌单二进制…"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o ghoul ./cmd/ghoul
  else
    warn "未检测到 npm：编译**不内嵌前端**的后端（前端需另跑 npm run dev）"
    CGO_ENABLED=0 go build -o ghoul ./cmd/ghoul
  fi
  ok "编译完成 → ./ghoul"
  warn "请通过 ./start.sh 重启正在运行的 ghoul 进程以生效（重启时会自动迁移 schema）"
}

echo "=============================="
echo "  GHOUL 更新"
echo "  1) Docker 更新（拉新镜像重建）"
echo "  2) 本地更新（go 重新编译）"
echo "=============================="
case "$(ask '选择' 1)" in
  1) sync_repo; update_docker ;;
  2) sync_repo; update_local ;;
  *) die "无效选择" ;;
esac
