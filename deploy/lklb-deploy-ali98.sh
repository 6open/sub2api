#!/usr/bin/env bash
set -euo pipefail

# LKLB ali98 快速部署脚本
#
# 默认执行主站部署：
#   1. 跑关键前端测试
#   2. 构建前端 dist
#   3. 用 Go 镜像构建 embed 二进制
#   4. 上传到 ali98
#   5. 备份并替换 sub2api 容器内 /app/sub2api
#   6. 重启容器并验证 /health、/purchase、/buy
#
# 常用：
#   ./deploy/lklb-deploy-ali98.sh
#   ./deploy/lklb-deploy-ali98.sh --backend-fast
#   ./deploy/lklb-deploy-ali98.sh --code-shop-only
#   ./deploy/lklb-deploy-ali98.sh --skip-tests

REMOTE_HOST="${REMOTE_HOST:-ali98}"
PUBLIC_BASE_URL="${PUBLIC_BASE_URL:-https://lklb.top}"
APP_CONTAINER="${APP_CONTAINER:-sub2api}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-sub2api-postgres}"
BUILD_IMAGE="${BUILD_IMAGE:-golang:1.26.5}"
GOPROXY="${GOPROXY:-https://goproxy.cn,https://goproxy.io,direct}"
GOSUMDB="${GOSUMDB:-sum.golang.google.cn}"
GO_BUILD_CACHE="${GO_BUILD_CACHE:-/tmp/sub2api-go-build-cache}"
GO_MOD_CACHE="${GO_MOD_CACHE:-/tmp/sub2api-go-mod-cache}"
FRONTEND_TEST_CMD="${FRONTEND_TEST_CMD:-corepack pnpm --dir frontend test -- --run src/views/user/__tests__/PaymentView.xianyu.static.spec.ts}"
FRONTEND_BUILD_CMD="${FRONTEND_BUILD_CMD:-corepack pnpm --dir frontend build}"
GO_BACKEND_TEST_CMD="${GO_BACKEND_TEST_CMD:-}"
REMOTE_HEALTH_URL="${REMOTE_HEALTH_URL:-http://127.0.0.1:18080/health}"
REMOTE_CODE_SHOP_DIR="${REMOTE_CODE_SHOP_DIR:-/opt/lklb-code-shop}"
REMOTE_CODE_SHOP_LOG="${REMOTE_CODE_SHOP_LOG:-/opt/lklb-code-shop/data/app.log}"

RUN_MAIN=1
RUN_CODE_SHOP=0
SKIP_TESTS=0
SKIP_FRONTEND_BUILD=0
SKIP_MAIN_VERIFY=0
RUN_BACKEND_CHECKS=0

usage() {
  cat <<EOF
Usage: $0 [options]

Options:
  --main-only             只部署 sub2api 主站（默认）
  --backend-fast          后端快速部署：跳过前端测试/build和页面校验，只跑后端检查、构建、健康检查
  --code-shop-only        只重启并验证 /buy 发码服务
  --restart-code-shop     主站部署后顺便重启 /buy 发码服务
  --skip-tests            跳过前端关键测试
  --skip-frontend-build   跳过前端 build（使用现有 backend/internal/web/dist）
  --skip-main-verify      跳过主站页面文案验证，只检查健康
  -h, --help              显示帮助

Environment:
  REMOTE_HOST=$REMOTE_HOST
  PUBLIC_BASE_URL=$PUBLIC_BASE_URL
  APP_CONTAINER=$APP_CONTAINER
  BUILD_IMAGE=$BUILD_IMAGE
  GOPROXY=$GOPROXY
  FRONTEND_TEST_CMD=$FRONTEND_TEST_CMD
  GO_BACKEND_TEST_CMD=${GO_BACKEND_TEST_CMD:-<default backend smoke tests>}
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --main-only)
      RUN_MAIN=1
      RUN_CODE_SHOP=0
      ;;
    --backend-fast)
      RUN_MAIN=1
      RUN_CODE_SHOP=0
      SKIP_TESTS=1
      SKIP_FRONTEND_BUILD=1
      SKIP_MAIN_VERIFY=1
      RUN_BACKEND_CHECKS=1
      ;;
    --code-shop-only)
      RUN_MAIN=0
      RUN_CODE_SHOP=1
      ;;
    --restart-code-shop)
      RUN_CODE_SHOP=1
      ;;
    --skip-tests)
      SKIP_TESTS=1
      ;;
    --skip-frontend-build)
      SKIP_FRONTEND_BUILD=1
      ;;
    --skip-main-verify)
      SKIP_MAIN_VERIFY=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

log() {
  printf '\n\033[1;34m==>\033[0m %s\n' "$*"
}

die() {
  echo "ERROR: $*" >&2
  exit 1
}

repo_root() {
  local dir
  dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  echo "$dir"
}

run_frontend_checks() {
  if [[ "$SKIP_TESTS" == "1" ]]; then
    log "跳过前端关键测试"
    return
  fi
  log "运行前端关键测试"
  eval "$FRONTEND_TEST_CMD"
}

build_frontend() {
  if [[ "$SKIP_FRONTEND_BUILD" == "1" ]]; then
    log "跳过前端 build"
    return
  fi
  log "构建前端 dist"
  eval "$FRONTEND_BUILD_CMD"
}

ensure_go_cache_dirs() {
  mkdir -p "$GO_BUILD_CACHE" "$GO_MOD_CACHE"
}

docker_go() {
  ensure_go_cache_dirs
  docker run --rm --network host \
    -e GOPROXY="$GOPROXY" \
    -e GOSUMDB="$GOSUMDB" \
    -e GOCACHE=/go-build-cache \
    -e GOMODCACHE=/go-mod-cache \
    -v "$GO_BUILD_CACHE":/go-build-cache \
    -v "$GO_MOD_CACHE":/go-mod-cache \
    "$@"
}

run_backend_checks() {
  if [[ "$RUN_BACKEND_CHECKS" != "1" ]]; then
    return
  fi

  if [[ -n "$GO_BACKEND_TEST_CMD" ]]; then
    log "运行自定义后端检查"
    eval "$GO_BACKEND_TEST_CMD"
    return
  fi

  log "运行后端快速检查"
  docker_go \
    -v "$PWD/backend":/src \
    -w /src \
    "$BUILD_IMAGE" \
    sh -lc '/usr/local/go/bin/go test -tags unit ./internal/service -run "TestComputeRuleMetric|TestComputeGroupAvailableRatio|TestCountAccountsByCondition" -count=1'
}

build_main_binary() {
  log "构建 sub2api embed 二进制"

  local out_host version_value date_value
  out_host="/tmp/sub2api-lklb-$(date +%Y%m%d%H%M%S)"
  version_value="$(tr -d '\r\n' < backend/cmd/server/VERSION)"
  date_value="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  docker_go \
    -v "$PWD":/src \
    -v /tmp:/host-tmp \
    -w /src/backend \
    "$BUILD_IMAGE" \
    sh -lc '/usr/local/go/bin/go version >/dev/null && CGO_ENABLED=0 GOOS=linux /usr/local/go/bin/go build -buildvcs=false -tags embed -ldflags="-s -w -X main.Version='"$version_value"' -X main.Commit=local-lklb-deploy -X main.Date='"$date_value"' -X main.BuildType=release" -trimpath -o /host-tmp/'"$(basename "$out_host")"' ./cmd/server'

  [[ -s "$out_host" ]] || die "构建产物不存在: $out_host"
  ls -lh "$out_host"
  echo "$out_host"
}

upload_and_install_main() {
  local bin_path
  bin_path="$1"
  local base
  base="$(basename "$bin_path")"

  log "上传二进制到 $REMOTE_HOST:/tmp/$base"
  scp "$bin_path" "$REMOTE_HOST:/tmp/$base"

  log "替换容器内主程序并重启"
  ssh "$REMOTE_HOST" \
    APP_CONTAINER="$APP_CONTAINER" \
    REMOTE_BIN="/tmp/$base" \
    REMOTE_HEALTH_URL="$REMOTE_HEALTH_URL" \
    'bash -s' <<'REMOTE'
set -euo pipefail

backup_main_binary() {
  local backup
  backup="/tmp/sub2api-backup-$(date +%Y%m%d%H%M%S)"
  podman cp "$APP_CONTAINER:/app/sub2api" "$backup"
  echo "$backup"
}

rollback_main_binary() {
  local backup="$1"
  echo "主站健康检查失败，回滚到: $backup" >&2
  podman cp "$backup" "$APP_CONTAINER:/app/sub2api"
  podman exec "$APP_CONTAINER" chmod +x /app/sub2api
  podman exec "$APP_CONTAINER" chown sub2api:sub2api /app/sub2api 2>/dev/null || true
  podman restart "$APP_CONTAINER" >/dev/null
}

wait_for_remote_health() {
  local i
  for i in $(seq 1 40); do
    if curl -fsS "$REMOTE_HEALTH_URL" >/tmp/sub2api-health.out 2>/tmp/sub2api-health.err; then
      cat /tmp/sub2api-health.out
      echo
      return 0
    fi
    sleep 1
  done
  cat /tmp/sub2api-health.err >&2 || true
  return 1
}

backup="$(backup_main_binary)"
podman cp "$REMOTE_BIN" "$APP_CONTAINER:/app/sub2api.new"
podman exec "$APP_CONTAINER" chmod +x /app/sub2api.new
podman exec "$APP_CONTAINER" chown sub2api:sub2api /app/sub2api.new 2>/dev/null || true
podman exec "$APP_CONTAINER" mv /app/sub2api /app/sub2api.prev.$(date +%Y%m%d%H%M%S)
podman exec "$APP_CONTAINER" mv /app/sub2api.new /app/sub2api
podman restart "$APP_CONTAINER" >/dev/null

if ! wait_for_remote_health; then
  rollback_main_binary "$backup"
  wait_for_remote_health
fi

podman ps --filter "name=$APP_CONTAINER" --format "{{.Names}} {{.Status}}"
REMOTE
}

wait_for_remote_health() {
  log "验证远端健康检查"
  ssh "$REMOTE_HOST" REMOTE_HEALTH_URL="$REMOTE_HEALTH_URL" 'bash -s' <<'REMOTE'
set -euo pipefail
for i in $(seq 1 30); do
  if curl -fsS "$REMOTE_HEALTH_URL"; then
    echo
    exit 0
  fi
  sleep 1
done
exit 1
REMOTE
}

verify_main_pages() {
  if [[ "$SKIP_MAIN_VERIFY" == "1" ]]; then
    log "跳过主站页面验证"
    return
  fi

  log "验证 /purchase 与 /buy"
  local asset
  asset="$(find backend/internal/web/dist/assets -maxdepth 1 -name 'PaymentView-*.js' -printf '%f\n' | head -1 || true)"
  [[ -n "$asset" ]] || die "找不到本地 PaymentView-*.js，无法验证线上资源"

  ssh "$REMOTE_HOST" \
    PUBLIC_BASE_URL="$PUBLIC_BASE_URL" \
    PAYMENT_ASSET="$asset" \
    'bash -s' <<'REMOTE'
set -euo pipefail

curl -k -s -o /tmp/lklb-purchase.html -w "purchase=%{http_code}\n" "$PUBLIC_BASE_URL/purchase"
curl -k -s -o /tmp/lklb-buy.html -w "buy=%{http_code}\n" "$PUBLIC_BASE_URL/buy"
curl -k -s -o /tmp/lklb-paymentview.js "$PUBLIC_BASE_URL/assets/$PAYMENT_ASSET"

grep -q "LinuxDO 积分购买" /tmp/lklb-paymentview.js
grep -q "前 10刀额度享特惠" /tmp/lklb-paymentview.js
grep -q "10 LDC = 1刀" /tmp/lklb-paymentview.js
grep -q "超出后按 50 LDC = 1刀" /tmp/lklb-paymentview.js
grep -q "自定义 LDC" /tmp/lklb-buy.html
grep -q "输入 LDC" /tmp/lklb-buy.html
echo "page_verify=ok PaymentView-$PAYMENT_ASSET"
REMOTE
}

restart_code_shop() {
  log "重启 /buy 发码服务"
  ssh "$REMOTE_HOST" \
    REMOTE_CODE_SHOP_DIR="$REMOTE_CODE_SHOP_DIR" \
    REMOTE_CODE_SHOP_LOG="$REMOTE_CODE_SHOP_LOG" \
    PUBLIC_BASE_URL="$PUBLIC_BASE_URL" \
    POSTGRES_CONTAINER="$POSTGRES_CONTAINER" \
    'bash -s' <<'REMOTE'
set -euo pipefail

cd "$REMOTE_CODE_SHOP_DIR"
sudo -n systemctl restart lklb-code-shop
for _ in $(seq 1 20); do
  if systemctl is-active --quiet lklb-code-shop; then
    break
  fi
  sleep 1
done
systemctl is-active lklb-code-shop
pgrep -a -u admin -f "^/usr/bin/python3.11 /opt/lklb-code-shop/app_stdlib.py$"

curl -k -s -o /tmp/lklb-buy-code-shop.html -w "buy=%{http_code}\n" "$PUBLIC_BASE_URL/buy"
grep -q "自定义 LDC" /tmp/lklb-buy-code-shop.html
grep -q "输入 LDC" /tmp/lklb-buy-code-shop.html
echo "code_shop_verify=ok"
REMOTE
}

deploy_main() {
  cd "$(repo_root)"
  run_frontend_checks
  build_frontend
  run_backend_checks

  local bin_path
  bin_path="$(build_main_binary | tail -1)"
  [[ -x "$bin_path" ]] || die "构建产物不可执行: $bin_path"

  upload_and_install_main "$bin_path"
  wait_for_remote_health
  verify_main_pages
}

main() {
  if [[ "$RUN_MAIN" == "1" ]]; then
    deploy_main
  fi

  if [[ "$RUN_CODE_SHOP" == "1" ]]; then
    restart_code_shop
  fi

  log "部署流程完成"
}

main "$@"
