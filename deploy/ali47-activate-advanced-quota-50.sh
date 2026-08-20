#!/usr/bin/env bash
set -euo pipefail

readonly DEPLOY_DIR=/root/lk/sub2api-deploy
readonly COMPOSE_FILE="$DEPLOY_DIR/docker-compose.yml"
readonly MIGRATION_NAME=229_raise_openai_advanced_weekly_quota.sql
readonly MIGRATION_CHECKSUM=fe79762dbb5db610350ddf6454a35cf57acca680517ca11d50ddcca0cb96bfcf

exec 9>/run/sub2api-advanced-quota-50.lock
flock -n 9 || exit 0

cd "$DEPLOY_DIR"

runtime_limit=$(docker inspect sub2api --format '{{range .Config.Env}}{{println .}}{{end}}' \
  | awk -F= '$1 == "GATEWAY_OPENAI_ADVANCED_QUOTA_WEEKLY_LIMIT_USD" { print $2 }')
migration_applied=$(
  printf "SELECT count(*) FROM schema_migrations WHERE filename = '%s';\n" "$MIGRATION_NAME" \
    | docker exec -i sub2api-postgres sh -lc \
      'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At'
)

if [[ "$runtime_limit" == "50" && "$migration_applied" == "1" ]]; then
  exit 0
fi

backup_dir="/root/sub2api-backups/pre-advanced-quota-50-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$backup_dir"
cp -a "$COMPOSE_FILE" "$backup_dir/"
docker exec sub2api-postgres sh -lc \
  'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -t user_platform_quotas -Fc' \
  > "$backup_dir/user_platform_quotas.dump"

if grep -Fq 'GATEWAY_OPENAI_ADVANCED_QUOTA_WEEKLY_LIMIT_USD=30' "$COMPOSE_FILE"; then
  sed -i \
    's/GATEWAY_OPENAI_ADVANCED_QUOTA_WEEKLY_LIMIT_USD=30/GATEWAY_OPENAI_ADVANCED_QUOTA_WEEKLY_LIMIT_USD=50/' \
    "$COMPOSE_FILE"
elif ! grep -Fq 'GATEWAY_OPENAI_ADVANCED_QUOTA_WEEKLY_LIMIT_USD=50' "$COMPOSE_FILE"; then
  echo "unexpected advanced quota configuration" >&2
  exit 1
fi

docker compose config -q

docker exec -i sub2api-postgres sh -lc \
  'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<SQL
BEGIN;
UPDATE user_platform_quotas
SET weekly_limit_usd = 50,
    updated_at = NOW()
WHERE platform = 'openai_advanced'
  AND deleted_at IS NULL
  AND weekly_limit_usd = 30;
INSERT INTO schema_migrations (filename, checksum)
VALUES ('$MIGRATION_NAME', '$MIGRATION_CHECKSUM')
ON CONFLICT (filename) DO NOTHING;
COMMIT;
SQL

docker compose up -d --no-deps sub2api

for _ in $(seq 1 30); do
  [[ "$(docker inspect -f '{{.State.Health.Status}}' sub2api 2>/dev/null || true)" == "healthy" ]] && exit 0
  sleep 2
done

echo "sub2api did not become healthy after activating the weekly quota" >&2
exit 1
