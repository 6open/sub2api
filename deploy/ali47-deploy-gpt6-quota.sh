#!/usr/bin/env bash
set -euo pipefail
cd /root/lk/sub2api-deploy
expected=sub2api:migo-gpt6-all-efforts-20260905
image=sub2api:migo-gpt6-fallback-20260905-final
test "$(docker inspect -f '{{.Config.Image}}' sub2api)" = "$expected"
docker image inspect "$image" >/dev/null
backup="/root/sub2api-backups/gpt6-quota-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$backup"
cp -a docker-compose.yml "$backup/"
docker exec sub2api-postgres pg_dump -U sub2api -d sub2api -Fc > "$backup/database.dump"
rollback() {
 cp -a "$backup/docker-compose.yml" docker-compose.yml
 docker compose up -d --no-deps sub2api
}
trap rollback ERR
sed -i "s|image: $expected|image: $image|" docker-compose.yml
docker compose config -q
docker compose up -d --no-deps sub2api
for i in $(seq 1 50); do
 if curl -fsS http://127.0.0.1:18080/health >/dev/null; then
  test "$(docker inspect -f '{{.Config.Image}}' sub2api)" = "$image"
  trap - ERR
  echo "deployed=$image backup=$backup"
  exit 0
 fi
 sleep 1
done
false
