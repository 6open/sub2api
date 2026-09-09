# 部署说明 — migo (ali47)

> 本文件用于快速确认「这份源码对应哪台线上服务」，避免用错仓库构建镜像。
> 最后更新：2026-09-09

## 这是什么

**migo 生产环境的源码工作树。**

| 项目 | 值 |
|---|---|
| 对应服务器 | `ali47` / `47.99.93.199` |
| 对外域名 | https://migo-ai.migomigo.com |
| 分支 | `deploy/ali47-v0.1.178-snapshot` |
| 部署目录 | `/root/lk/sub2api-deploy` |
| 数据库迁移覆盖至 | `234_encrypt_prompt_audit_full_prompt.sql` |

## 校验方法

构建前务必确认源码与线上数据库匹配，否则容器会因迁移校验失败而无法启动：

```bash
# 1. 线上数据库最新迁移
ssh ali47 "docker exec sub2api-postgres psql -U sub2api -d sub2api \
  -c \"select filename, applied_at from schema_migrations order by applied_at desc limit 3;\""

# 2. 本仓库最新迁移（应能覆盖上面的结果）
ls backend/migrations/*.sql | tail -3
```

迁移 checksum 算法为 `sha256(TrimSpace(文件内容))`，可用它精确比对：

```bash
python3 -c "
import hashlib
c=open('backend/migrations/<文件名>.sql','rb').read().decode()
print(hashlib.sha256(c.strip().encode()).hexdigest())"
```

## 构建与部署流程

```bash
# 1. 确认工作区干净（未提交改动先 stash，见下方「未提交改动」）
git status --short

# 2. 构建
TAG="sub2api:migo-<用途>-$(date +%Y%m%d-%H%M)"
docker build -f deploy/Dockerfile -t "$TAG" .

# 3. 传输到服务器
docker save "$TAG" | gzip -1 | ssh ali47 "gunzip | docker load"

# 4. 【重要】先用临时容器验证迁移校验通过，再切生产
ssh ali47 "cd /root/lk/sub2api-deploy && timeout 60 docker run --rm \
  --network sub2api-deploy_sub2api-network --env-file .env \
  -v /root/lk/sub2api-deploy/data:/app/data \$TAG 2>&1 \
  | grep -iE 'checksum|Failed to initialize|Server started'"
# 期望看到 "Server started"，若出现 checksum mismatch 则禁止部署

# 5. 切换镜像
ssh ali47 "cd /root/lk/sub2api-deploy && \
  cp docker-compose.yml docker-compose.yml.bak-\$(date +%Y%m%d-%H%M%S) && \
  sed -i 's|image: sub2api:.*|image: '\$TAG'|' docker-compose.yml && \
  docker compose up -d sub2api"
```

## 已知的坑

**迁移文件不可修改。** 已应用的迁移改内容会导致 checksum 不匹配、容器无法启动。新变更一律新建迁移文件。
镜像 `migo-gpt6-fallback-20260905-final` 和 `migo-key-quota-isolation-20260907` 就因为改了
`197_channel_monitor_v2_seed_popular_models.sql` 而无法启动，详见 `docs/migration-197-incident.md`。

**问题只在重启时暴露。** 迁移校验仅在进程启动时执行，容器持续运行期间不会发现问题。
所以「换了镜像但没重启」时一切正常，一旦重启就崩溃循环。

**账号 model_mapping 为空 = 放行所有模型。** 若给原本为 `null` 的账号写入只含少数模型的映射，
会导致其余模型被判定为「不支持」，触发 `Model "xxx" is not supported by any configured account in this group`。
补充映射前先确认原值。

## 未提交改动

工作区可能存在未提交的线上定制代码。构建前用 `git status --short` 确认：

- 若改动属于当前线上运行的功能 → 保留并一起构建
- 若属于开发中/实验性质 → `git stash push -m "描述"` 后再构建

2026-09-09 曾 stash 一批 key-quota-isolation 相关改动（`stash@{0}`），尚未上线。

## 相关仓库对照

| 目录 | 分支 | 用途 |
|---|---|---|
| `sub2api-upgrade-ali47` | `deploy/ali47-v0.1.178-snapshot` | **migo 线上源码（本目录）** |
| `sub2api-edge-control` | `deploy/ali98-edge-control-20260819` | lklb 线上源码 |
| `sub2api` | `lklb/main` | 旧基础仓（停在 2026-07-21，迁移仅到 198），**不可用于构建** |
| `sub2api-upgrade-ali98` | `deploy/ali98-v0.1.178-snapshot` | 旧开发快照，**名字有误导性，不是线上源码** |
| `sub2api-bwg-node` | `dev/bwg-edgenode-20260819` | US 边缘网关源码（bwg / us.lklb.top） |
