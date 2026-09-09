# 部署说明 — bwg US 边缘节点

> 本文件用于快速确认「这份源码对应哪台线上服务」，避免用错仓库构建。
> 最后更新：2026-09-09

## 这是什么

**US 边缘网关（`lklb-us-gateway`）的源码工作树。**

注意：bwg 机器上**没有**跑完整的 sub2api 服务，只跑一个独立的 Go 网关二进制。
鉴权、账号调度、计费仍由 lklb 主站（ali98）承担。

| 项目 | 值 |
|---|---|
| 对应服务器 | `bwg` / `45.78.79.47` |
| 对外域名 | https://us.lklb.top |
| 分支 | `dev/bwg-edgenode-20260819` |
| 核心源码 | `backend/internal/pkg/edgenode/`（当前为未跟踪状态） |
| 线上二进制 | `/opt/lklb-us-gateway/gateway`（2026-09-08 构建） |
| systemd 服务 | `lklb-us-gateway.service`、`lklb-us-link.service` |

## 请求链路

```
用户 → us.lklb.top
     → nginx (443/8443/9443)
     → lklb-us-gateway (127.0.0.1:19445)
     → SSH 隧道 (127.0.0.1:29445)
     → ali98:18445 → lklb-edge-control 容器
```

主站只负责 `prepare`（鉴权 + 选账号）与 `settle`（计费），
**实际的 OpenAI 调用由 bwg 本地直接发出**，出口即 `45.78.79.47`。

## 机器资源约束

内存仅 1 GB，网关已按此调优，修改配置时注意不要突破：

```
MemoryHigh=220M   MemoryMax=300M   GOMEMLIMIT=192MiB
CPUQuota=75%      TasksMax=128     LimitNOFILE=1024
```

## 相关配置

| 项目 | 位置 |
|---|---|
| 网关配置 | `/etc/lklb-us-gateway/config.json`（含 token / public_key，权限受控） |
| 请求状态目录 | `/var/lib/lklb-us-gateway/requests/`（每请求一个 JSON，含 outcome / error_code） |
| 隧道密钥 | `/etc/lklb-us-gateway/link-key` |

排查请求失败时，`/var/lib/lklb-us-gateway/requests/<request_id>.json` 里有
`outcome`、`error_code`、`Diagnostic` 字段，比日志更直接。

## 已知问题

**上游过载表现为「断流」而非报错。** OpenAI 容量紧张时会先返回 HTTP 200 正常推流，
中途插入 error 事件后终止，不发送终止 usage 包。网关记为
`edge_stream_incomplete / missing_terminal_usage`，用户侧看到的是「输出到一半卡住」。
请求记录里 `error_code = server_is_overloaded` 可确认此情况，属上游问题。

**该机器无法使用 WestData / 台湾等国内订阅线路。** 这些服务商只放行国内来源 IP，
从美国访问其入口 TCP/ICMP 全部超时。bwg 直连 OpenAI 仅约 73ms，本就无需代理。

## 相关仓库对照

| 目录 | 分支 | 用途 |
|---|---|---|
| `sub2api-bwg-node` | `dev/bwg-edgenode-20260819` | **US 边缘网关源码（本目录）** |
| `sub2api-edge-control` | `deploy/ali98-edge-control-20260819` | lklb 线上源码（本节点的上游主站） |
| `sub2api-upgrade-ali47` | `deploy/ali47-v0.1.178-snapshot` | migo 线上源码 |
| `sub2api` | `lklb/main` | 旧基础仓（停在 2026-07-21，迁移仅到 198），**不可用于构建** |
| `sub2api-upgrade-ali98` | `deploy/ali98-v0.1.178-snapshot` | 旧开发快照，**名字有误导性，不是线上源码** |
