#!/usr/bin/env bash
set -Eeuo pipefail

readonly IMAGE="${1:?usage: $0 <image>}"
readonly MODE="${2:-deploy}"
readonly DEPLOY_DIR=/root/lk/sub2api-deploy
readonly COMPOSE_FILE="$DEPLOY_DIR/docker-compose.yml"
readonly CONTAINER=sub2api
readonly BACKUP_DIR="/root/sub2api-backups/pre-combined-quota-fix-$(date +%Y%m%d-%H%M%S)"

exec 9>/run/sub2api-combined-quota-fix.lock
flock -n 9 || exit 0

verify_image() {
  docker image inspect "$IMAGE" >/dev/null
  docker run --rm --entrypoint sh "$IMAGE" -c \
    "strings /app/sub2api | grep -Fq 'incr advanced quota DB failed'"
  docker run --rm --entrypoint sh "$IMAGE" -c \
    "strings /app/sub2api | grep -Fq 'upstream returned 2xx without a compaction output item'"
  docker run --rm --entrypoint sh "$IMAGE" -c \
    "strings /app/sub2api | grep -Fq 'user.openai_advanced_quota_self_reset'"
}

wait_healthy() {
  local expected_image="$1"
  for _ in $(seq 1 60); do
    if [[ "$(docker inspect -f '{{.State.Health.Status}}' "$CONTAINER" 2>/dev/null || true)" == "healthy" ]] &&
       [[ "$(docker inspect -f '{{.Config.Image}}' "$CONTAINER" 2>/dev/null || true)" == "$expected_image" ]]; then
      return 0
    fi
    sleep 2
  done
  return 1
}

rollback() {
  local status=$?
  trap - ERR
  if [[ -f "$BACKUP_DIR/docker-compose.yml" ]]; then
    cp -a "$BACKUP_DIR/docker-compose.yml" "$COMPOSE_FILE"
    cd "$DEPLOY_DIR"
    docker compose up -d --no-deps "$CONTAINER" || true
  fi
  exit "$status"
}

verify_image
if [[ "$MODE" == "--verify-only" ]]; then
  exit 0
fi
if [[ "$MODE" != "deploy" ]]; then
  echo "unknown mode: $MODE" >&2
  exit 2
fi

mkdir -p "$BACKUP_DIR"
cp -a "$COMPOSE_FILE" "$BACKUP_DIR/"
docker exec sub2api-postgres sh -lc \
  'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -t user_platform_quotas -t groups -Fc' \
  > "$BACKUP_DIR/quota-and-groups.dump"

trap rollback ERR

cd "$DEPLOY_DIR"
current_image=$(docker inspect -f '{{.Config.Image}}' "$CONTAINER")
sed -i "0,/image: ${current_image}/s//image: ${IMAGE}/" "$COMPOSE_FILE"
docker compose config -q
docker compose up -d --no-deps "$CONTAINER"
wait_healthy "$IMAGE"

docker exec -i sub2api-postgres sh -lc \
  'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
BEGIN;

INSERT INTO user_platform_quotas (
    user_id, platform, weekly_limit_usd, weekly_usage_usd,
    weekly_window_start, created_at, updated_at
)
SELECT
    id, 'openai_advanced', 50, 0,
    date_trunc('week', NOW() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai',
    NOW(), NOW()
FROM users
WHERE deleted_at IS NULL AND role <> 'admin'
ON CONFLICT (user_id, platform) WHERE deleted_at IS NULL DO NOTHING;

WITH weekly_usage AS (
    SELECT ul.user_id, COALESCE(SUM(ul.actual_cost), 0) AS usage_usd
    FROM usage_logs ul
    JOIN users u ON u.id = ul.user_id AND u.deleted_at IS NULL AND u.role <> 'admin'
    JOIN groups g ON g.id = ul.group_id AND g.deleted_at IS NULL AND g.platform = 'openai'
    WHERE ul.created_at >=
          date_trunc('week', NOW() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai'
      AND lower(COALESCE(ul.reasoning_effort, '')) IN ('high', 'xhigh', 'x-high', 'x_high', 'max')
      AND lower(COALESCE(ul.model, '')) NOT LIKE '%terra%'
      AND lower(COALESCE(ul.model, '')) NOT LIKE '%luna%'
    GROUP BY ul.user_id
)
UPDATE user_platform_quotas q
SET weekly_usage_usd = COALESCE(w.usage_usd, 0),
    weekly_window_start =
        date_trunc('week', NOW() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai',
    daily_window_start = COALESCE(
        q.daily_window_start,
        date_trunc('day', NOW() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai'
    ),
    monthly_window_start = COALESCE(q.monthly_window_start, NOW()),
    updated_at = NOW()
FROM users u
LEFT JOIN weekly_usage w ON w.user_id = u.id
WHERE q.user_id = u.id
  AND q.platform = 'openai_advanced'
  AND q.deleted_at IS NULL
  AND u.deleted_at IS NULL
  AND u.role <> 'admin';

UPDATE groups
SET max_reasoning_effort = '', updated_at = NOW()
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND max_reasoning_effort = 'medium';

INSERT INTO auth_cache_invalidation_outbox (cache_key)
SELECT DISTINCT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
FROM api_keys k
JOIN groups g ON g.id = k.group_id
WHERE k.deleted_at IS NULL
  AND k.key <> ''
  AND g.deleted_at IS NULL
  AND g.platform = 'openai';

COMMIT;
SQL

trap - ERR

docker inspect "$CONTAINER" --format '{{.Config.Image}} {{.State.Status}} {{.State.Health.Status}} {{.RestartCount}}'
docker exec sub2api-postgres sh -lc \
  'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "SELECT weekly_window_start, round(sum(weekly_usage_usd)::numeric, 4) FROM user_platform_quotas WHERE platform = '\''openai_advanced'\'' AND deleted_at IS NULL GROUP BY weekly_window_start ORDER BY weekly_window_start DESC LIMIT 1"'
