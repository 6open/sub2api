# 部署说明 — lklb (ali98)

> 本文件用于快速确认「这份源码对应哪台线上服务」，避免用错仓库构建镜像。
> 最后更新：2026-09-09

## 这是什么

**lklb 生产环境的源码工作树。**

| 项目 | 值 |
|---|---|
| 对应服务器 | `ali98` / `139.224.110.98` |
| 对外域名 | https://lklb.top ，US 边缘入口 https://us.lklb.top |
| 分支 | `deploy/ali98-edge-control-20260819` |
| 线上镜像 | `localhost/lklb-sub2api:v0178-rc1-20260819` |
| 部署目录 | `/home/admin/sub2api-deploy` |
| 容器运行时 | **podman**（非 docker，注意命令差异） |
| 数据库迁移覆盖至 | `227_lklb_usage_outcomes.sql` |

## 如何确认这是正确的源码

`227_lklb_usage_outcomes.sql` 是决定性标志——只有本工作树含该文件，
其余同源工作树（`sub2api-upgrade-ali98`、`sub2api-bwg-node`）都只到 226。
线上数据库已于 2026-09-08 应用 227，用缺失该文件的源码构建会导致启动校验失败。

```bash
# 确认本仓库有 227
ls backend/migrations/227*.sql

# 确认线上数据库迁移状态
ssh ali98 "docker exec sub2api-postgres psql -U sub2api -d sub2api \
  -c \"select filename, applied_at from schema_migrations order by applied_at desc limit 3;\""
```

## 构建与部署流程

```bash
# 1. 确认工作区状态（当前有约 31 个未提交改动，含 227 迁移，属线上运行代码，勿随意丢弃）
git status --short

# 2. 构建
TAG="lklb-sub2api:<用途>-$(date +%Y%m%d-%H%M)"
docker build -f deploy/Dockerfile -t "$TAG" .

# 3. 传输
docker save "$TAG" | gzip -1 | ssh ali98 "gunzip | podman load"

# 4. 【重要】先验证迁移校验，再切生产
# 5. 切换镜像后重启：
ssh ali98 "cd /home/admin/sub2api-deploy && docker compose up -d sub2api"
```

## 网络出口架构

lklb 容器位于 `10.89.1.0/24`，**到不了宿主机的 WireGuard 内网地址**（如 `10.88.0.1`、`10.77.0.1` 需经中继）。
现有出口及其接入方式：

| 出口 | 容器可达地址 | 说明 |
|---|---|---|
| DMIT LAX | `172.24.24.193:7899` | 经本机 socat 转发 → `10.88.0.1:7890`，**需 Basic 认证** |
| BWG LA | `10.77.0.1:17890` | 经 migo 的 WireGuard 中继 |
| WestData | `10.77.0.1:17893` | 经 migo 的 WireGuard 中继 |
| 台湾 miyavip | `top-vip.miyavip.vip:8001` | 公网 socks5h，容器可直连 |

相关 systemd 服务（均已 enable 开机自启）：

- 本机：`dmit-proxy-relay.service`
- migo 上：`migo-bwg-wireguard.service`、`migo-westdata-wireguard.service`、`migo-taiwan-wireguard.service`
- 本机→migo 方向：`lklb-dmit-to-migo.service`

## 已知的坑

**DMIT 代理需要认证。** 上游是 tinyproxy，无认证访问返回 `407 Proxy Authentication Required`。
凭据存于 `proxies` 表的 `username` / `password` 字段。曾因裸 curl 测试（未带凭据）
误判为「代理失效」，进而误将账号解绑代理，导致错误率上升。

**容器到不了 WireGuard 内网。** 配置代理时必须使用容器可达地址（见上表），
直接填 `10.88.0.1` 之类会静默失败。

**迁移文件不可修改。** 已应用的迁移改内容会导致 checksum 不匹配、容器无法启动。

## 待办

- Open WebUI 生图仍配置为 `gpt-image-2`，尚未切到 `gpt-image-2.5`
- 切换前需先移植 migo 的驱动模型修复：`backend/internal/service/openai_images.go`
  中 `openAIImagesResponsesMainModel` 硬编码为 `gpt-5.4-mini`，
  ChatGPT 订阅账号在 Codex 通道下无权调用该模型，需改为 `gpt-5.6-sol`
  （migo 已改为可通过环境变量 `OPENAI_IMAGES_RESPONSES_MAIN_MODEL` 覆盖）
- 账号 `0911` 缺少 `gpt-image-2` 映射，四账号生图能力不一致
- `channel_model_pricing` 中 luna 那条记录（$1.25/$7.5）实际未生效，
  真实计费走代码内置值（$1/$6），属僵尸配置，建议清理

## 相关仓库对照

| 目录 | 分支 | 用途 |
|---|---|---|
| `sub2api-edge-control` | `deploy/ali98-edge-control-20260819` | **lklb 线上源码（本目录）** |
| `sub2api-upgrade-ali47` | `deploy/ali47-v0.1.178-snapshot` | migo 线上源码 |
| `sub2api` | `lklb/main` | 旧基础仓（停在 2026-07-21，迁移仅到 198），**不可用于构建** |
| `sub2api-upgrade-ali98` | `deploy/ali98-v0.1.178-snapshot` | 旧开发快照，**名字有误导性，不是线上源码** |
| `sub2api-bwg-node` | `dev/bwg-edgenode-20260819` | US 边缘网关源码（bwg / us.lklb.top） |
