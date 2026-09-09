# 迁移 197 冲突事故记录 — migo (ali47)

事发日期：2026-09-09
文档更新：2026-09-09 晚（初版写于当日 16:35，事后已修正状态）
状态：**服务已恢复，根因未清除**

---

## 一句话总结

迁移文件 `197_channel_monitor_v2_seed_popular_models.sql` 在被数据库执行后又被修改，
导致 checksum 不匹配。sub2api 有迁移不可变性校验，**启动即失败**，容器陷入崩溃循环。

---

## 事故经过

| 时间 | 事件 |
|---|---|
| 2026-08-14 23:52 | 197 迁移正常应用，数据库记录 checksum `23d0489c…` |
| 2026-09-05 / 09-07 | 两次构建镜像，其中 197 文件已被改动（checksum 变为 `696f40ce…`） |
| — | 容器持续运行未重启，**问题潜伏未暴露** |
| 09-09 16:04:10 | 有人手动停止容器（日志 `hasBeenManuallyStopped=true`），重启即崩溃 |
| 09-09 16:12 | 回退到 `migo-gpt6-fallback-20260905-final` —— **仍然崩溃**（该镜像同样含被改动的 197） |
| 09-09 16:29:17 | 再次被手动停止，再次崩溃循环 |
| 09-09 16:33 | 回退到 `migo-v0.1.178-prompt-privacy-20260901` —— 启动成功，服务恢复 |
| 09-09 16:54 | 基于本仓库源码构建 `migo-image25-driver-20260909-1651` 并部署，**当前运行中** |

## checksum 对照

| 来源 | checksum |
|---|---|
| 数据库记录（2026-08-14 应用时写入） | `23d0489c1b421bc6d7c91bbfcb7006eb49a0d107f1e5ee441cc6502f8b280cbb` |
| 09-05 / 09-07 镜像内的文件 | `696f40ce8fcd0ac907d8986b8098e786af9678ada83145a1d5f5cb94e41f29b0` |
| **本仓库（sub2api-upgrade-ali47）** | `23d0489c…` ✅ **与数据库一致** |

校验算法：`sha256(TrimSpace(文件内容))`

```bash
python3 -c "
import hashlib
c=open('backend/migrations/197_channel_monitor_v2_seed_popular_models.sql','rb').read().decode()
print(hashlib.sha256(c.strip().encode()).hexdigest())"
```

## 受影响镜像

| 镜像 | 能否启动 |
|---|---|
| `migo-key-quota-isolation-20260907` | ❌ checksum 冲突 |
| `migo-gpt6-fallback-20260905-final` | ❌ checksum 冲突 |
| `migo-v0.1.178-prompt-privacy-20260901` | ✅ |
| **`migo-image25-driver-20260909-1651`** | ✅ **当前线上运行** |

---

## 为什么潜伏两天才爆发

迁移校验**只在进程启动时执行**。09-07 换镜像后容器一直运行未重启，问题未被触发。
09-09 容器被手动停止两次，每次重启都触发校验并崩溃。

> **风险提示**：任何含被改动 197 的镜像，只要重启就会复现。

---

## 当前状态

线上运行的 `migo-image25-driver-20260909-1651` 基于**本仓库**构建，
其 197 文件 checksum 与数据库一致，**不受此问题影响**。

但那两个旧镜像仍然无法启动，且根因（197 被谁、为何修改）**尚未查清**。

---

## 若要恢复 09-05 / 09-07 镜像中的功能

### 推荐路径：基于本仓库重新构建

本仓库的 197 是正确版本。把需要的功能改动移植过来重新构建即可，
无需处理 checksum 问题。

`feature/key-quota-isolation` 分支已固化 09-07 那批改动（尚未上线），
可从该分支 cherry-pick。注意：**该分支本身未触碰迁移文件**（已核对），
若原开发确实改过 197，需查清改了什么，并按规范新建迁移文件承载变更。

### 不推荐：直接改数据库 checksum

```sql
-- 仅在确认新旧文件差异无害（如纯 seed 数据补充、不含结构变更）时才可考虑
UPDATE schema_migrations
SET checksum = '696f40ce8fcd0ac907d8986b8098e786af9678ada83145a1d5f5cb94e41f29b0'
WHERE filename = '197_channel_monitor_v2_seed_popular_models.sql';
```

风险：若被改动的内容含结构变更，数据库实际状态将与迁移记录不符，埋下隐患。
执行前必须先 diff 新旧文件内容。

---

## 教训

**迁移文件一旦应用就不可修改。** 新变更一律新建迁移文件。

**问题只在重启时暴露。** "换了镜像但服务正常"不代表镜像没问题，
可能只是还没重启过。部署新镜像后应主动重启一次验证。

**部署前用临时容器预检。** 见 `../DEPLOY.md` 的构建流程第 4 步，
可在不影响现网的前提下发现此类问题：

```bash
ssh ali47 "cd /root/lk/sub2api-deploy && timeout 60 docker run --rm \
  --network sub2api-deploy_sub2api-network --env-file .env \
  -v /root/lk/sub2api-deploy/data:/app/data <镜像tag> 2>&1 \
  | grep -iE 'checksum|Failed to initialize|Server started'"
```

期望输出 `Server started`；若出现 `checksum mismatch` 则禁止部署。
