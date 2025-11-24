# Tracker 侧集成指南（面向 Tracker / Chihaya）

## 概述
- 目标：与 Chihaya Tracker 集成，实现用户授权（passkey 白名单）、数据采集（流量、指标）与风控审计。
- 对齐点：Redis 白名单集合、HTTP 回源校验接口、Redis Streams 流量推送消费、审计与失败重试机制。

## 基本约定
- passkey 统一使用小写十六进制字符串。
- 所有回源 HTTP 接口必须走 HTTPS 并携带 `X-API-Key`。
- Tracker 侧对回源结果建议设置缓存 TTL（如 300s）与限流。

## Redis：passkey 白名单集合
- 键：`pt:passkeys`
- 结构：Redis Set，成员为 `passkey`
- 常用操作：
  - 添加：`SADD pt:passkeys <passkey>`
  - 删除：`SREM pt:passkeys <passkey>`
  - 校验：`SISMEMBER pt:passkeys <passkey>` → `1` 命中
- 原子全量刷新：
  - 重建新集合：持续 `SADD pt:passkeys:new <passkey...>`
  - 切换：`RENAME pt:passkeys pt:passkeys:old` → `RENAME pt:passkeys:new pt:passkeys`

## 回源校验 API（Redis 未命中时）
- 路径：`GET /passkey/verify?passkey=<passkey>`
- 认证：请求头 `X-API-Key: <TRACKER_API_KEY>`（站点配置项）
- 返回：`200 OK`，`{"valid": true|false}`；`401` 为鉴权失败；`400` 为参数非法。
- 行为：先查 Redis 集合，未命中则查数据库用户表 `passkey` 字段。
- 示例：
```
curl -s \
  -H "X-API-Key: ${TRACKER_API_KEY}" \
  "https://pt.example.com/passkey/verify?passkey=abcdef012345..."
# {"valid": true}
```

## Streams：Tracker→PT 流量推送
- 主键名：默认 `tracker:traffic`，可由 `TRACKER_STREAM_KEY` 修改。
- 消费组：`XGROUP CREATE tracker:traffic ztorrent $`
- 读取：`XREADGROUP GROUP ztorrent consumer-1 COUNT 100 BLOCK 5000 STREAMS tracker:traffic >`
- 字段列表：
  - `passkey`：用户密钥（字符串）
  - `infohash`：种子哈希（40位十六进制）
  - `peer_id`：客户端 PeerID（40位十六进制）
  - `port`：客户端监听端口（整数）
  - `ip`：客户端 IP（如 `1.2.3.4` 或 `2001:db8::1`）
  - `af`：`IPv4` 或 `IPv6`
  - `du`：本次上传增量字节（无符号整数）
  - `dd`：本次下载增量字节（无符号整数）
  - `left`：当前剩余未下载字节（无符号整数）
  - `event`：`none|started|completed|stopped`
  - `ts`：本次 Announce 时间戳（秒）
  - `dt`：与上次 Announce 的秒差（无上次则为 0）
  - `interval`：本次响应建议的 announce 间隔（秒）
  - `min_interval`：本次响应建议的最小间隔（秒）
- 聚合逻辑（PT 侧消费者）：
  - 用户定位：`passkey` → 数据库 `users.passkey`
  - 种子定位：`infohash` → 数据库 `torrents.infoHash`
  - 增量累计：`uploaded += du`，`downloaded += dd`（以字符串存储，内部使用 BigInt 累加）
  - 做种状态：`event=stopped → isSeeding=false`；`event=completed` 或 `left=0 → isSeeding=true`
  - 做种时长：优先使用 `dt`，否则用 `interval`；在 `isSeeding=true` 时累加。
  - 完成标记：`event=completed → isCompleted=true`

## 幂等与去重建议（Tracker 侧）
- 幂等键：`passkey + infohash + peer_id + 时间窗口`。
- 对重复事件做窗口累计与速率计算（如 `du/dt`、`dd/dt`）。
- 收敛错误与异常上报频率（例如异常大的 `du/dd`）并做风控策略。

## 死信队列与重试（PT 侧消费者）
- DLQ 键：默认 `tracker:traffic:dlq`（`TRACKER_DLQ_KEY`）
- 失败流：默认 `tracker:traffic:failed`（`TRACKER_FAILED_KEY`）
- 主消费失败时：写入 DLQ 并 `XACK` 主消息。
- DLQ 重试：读取 DLQ，`retry` 次数未达上限（`TRACKER_RETRY_MAX`，默认 3）则重放；超过上限写入失败流。
- 监控指标：队列滞留长度、重试次数分布、失败流增长速率。

## 安全与规范
- HTTPS：所有回源 HTTP 必须使用 HTTPS。
- 鉴权：回源接口必须校验 `X-API-Key`；建议 Tracker 侧对接特定服务账号。
- 编码：passkey 建议统一大小写与编码格式，避免差异导致误判。
- 限流：回源接口与 Streams 消费建议都具备限流与审计能力。

## 配置项（PT 侧）
- `TRACKER_API_KEY`：回源接口鉴权密钥。
- `TRACKER_STREAM_KEY`：默认 `tracker:traffic`。
- `TRACKER_DLQ_KEY`：默认 `tracker:traffic:dlq`。
- `TRACKER_FAILED_KEY`：默认 `tracker:traffic:failed`。
- `TRACKER_RETRY_MAX`：默认 `3`。
- Redis 连接：`REDIS_HOST`、`REDIS_PORT`、`REDIS_PASSWORD`、`REDIS_DB`。

## 审计
- 记录范围：白名单增删、全量刷新。
- 字段：`action`、`actorId`、`subjectType`、`subjectId`、`result`、`details`、`at`。
- 存储：追加到 `logs/audit.log`（JSON Lines）。

## Chihaya 示例（思路参考）
- passkey 授权 prehook：
  - 先 `SISMEMBER pt:passkeys`，未命中则 `GET /passkey/verify?passkey=`（含 `X-API-Key`）。
  - 命中回源则可异步 `SADD` 回填。
- 流量推送：在 announce 处理后，将增量事件写入 Streams：
```
XADD tracker:traffic * \
  passkey <passkey> infohash <infohash> peer_id <peer_id> \
  port <port> ip <ip> af <IPv4|IPv6> \
  du <delta_uploaded> dd <delta_downloaded> left <left> \
  event <none|started|completed|stopped> ts <timestamp_sec> \
  dt <delta_sec> interval <interval_sec> min_interval <min_interval_sec>
```

## 故障与回退
- 回源接口故障：短期依赖 Redis 白名单；必要时降级到严格拒绝或只允许命中缓存的 passkey。
- 消费积压：扩大消费者并发与 `COUNT`，同时检查 DLQ 重试与失败流增长；视情况暂停非关键业务推送。

## 联系与联调
- 提供测试 passkey 与样例 infohash 以便联调。
- 约定联调窗口与回源限流阈值，避免短时间洪峰压测导致拒绝服务。
