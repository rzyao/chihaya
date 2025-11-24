# PT 侧集成指南（面向站点）

## 概述
- 目标：与 Chihaya Tracker 集成，实现用户授权（passkey）、数据采集（流量、指标）与运营风控
- 本指南列出 PT 侧需提供/维护的接口与服务，以及字段和约束

## 必要配合项

- passkey 白名单维护（用于路由或查询授权）
  - Redis 集合：`Set pt:passkeys`
    - 成员：纯字符串 `passkey`（建议统一小写十六进制）
    - 操作：`SADD pt:passkeys <passkey>`、`SREM pt:passkeys <passkey>`、`SISMEMBER pt:passkeys <passkey>`
    - 初始化与全量刷新（原子切换）：`RENAME pt:passkeys pt:passkeys:old`→`RENAME pt:passkeys:new pt:passkeys`
  - 可选：回源校验接口（当 Redis 未命中时）
    - `GET https://pt.example.com/api/passkey/verify?passkey=<passkey>`
    - 响应：`2xx` 且 `{"valid": true}` 视为通过；其他视为拒绝
    - 建议开启鉴权（Token 或签名）、限流与审计；返回语义清晰

- 消费 Redis Streams（Tracker→PT 的流量推送）
  - Stream 键：默认 `tracker:traffic`（可配置）
  - 消费方式：消费者组（建议）`XGROUP CREATE tracker:traffic group-pt $`，`XREADGROUP GROUP group-pt consumer-1 COUNT 100 STREAMS tracker:traffic >`
  - 字段与语义：
    - `passkey`：用户密钥
    - `infohash`：种子哈希（十六进制，40位）
    - `peer_id`：客户端PeerID（十六进制，40位）
    - `port`：客户端端口（整数）
    - `ip`：客户端IP（字符串）
    - `af`：地址族（`IPv4`/`IPv6`）
    - `du`：上传增量字节（无符号整数）
    - `dd`：下载增量字节（无符号整数）
    - `left`：剩余未下载字节（无符号整数）
    - `event`：`none|started|completed|stopped`
    - `ts`：本次Announce时间戳（秒）
    - `dt`：与上次Announce的秒差（无上次则为0）
    - `interval`：响应建议的间隔（秒）
    - `min_interval`：响应建议的最小间隔（秒）
  - 幂等聚合建议：以 `passkey+infohash+peer_id+时间窗口` 作为幂等键；对重复事件去重，做窗口累计与速率计算（`du/dt`、`dd/dt`）
  - 异常与回退：构建DLQ与告警；对长时间积压与失败重试进行监控

- Prometheus 指标拉取（PT 监控）
  - 入口：`http://<metrics_addr>/metrics`
  - 指标：
    - `chihaya_http_response_duration_milliseconds{action,address_family,error}`
    - `chihaya_udp_response_duration_milliseconds{action,address_family,error}`
    - `chihaya_storage_infohashes_count`、`chihaya_storage_seeders_count`、`chihaya_storage_leechers_count`
    - `chihaya_storage_gc_duration_milliseconds`
  - 用途：集群健康、性能与容量监控；非用户级别结算

## 路由与客户端
- Announce 路由建议：`/announce/:passkey`（命名参数）
  - Tracker 会从路由注入 `passkey` 到上下文；未提供时也可从查询参数读取
- 客户端 Announce 示例（HTTP）：
  - `https://tracker.example/announce/<passkey>?info_hash=<20字节%编码>&peer_id=<20字节%编码>&port=6881&uploaded=...&downloaded=...&left=...&event=started&numwant=50&compact=1`

- Scrape 请求方式与参数格式：
  - HTTP：`GET /scrape?info_hash=<20字节%编码>[&info_hash=<20字节%编码>...]`
    - `info_hash` 为原始 20 字节（二进制）经 URL 百分号编码；可重复出现以批量查询
    - 响应为 Bencode：`{"files": { <raw 20字节作为键>: {"complete": <做种数>, "incomplete": <下载数>} }}`
    - 受配置项 `max_scrape_infohashes` 限制最大数量（示例配置在 `dist/example_config.yaml`）
  - UDP（BEP 15）：`action=2`，负载为多个顺序拼接的 20 字节 `info_hash`
    - 响应按序写入：`complete`、`snatches`、`incomplete`
  - 说明：Scrape 为只读查询，不返回 peers，不改变 swarm 状态

## 安全与规范
- TLS：所有 PT 提供的 HTTP 接口（passkey 回源）必须使用 HTTPS
- 访问控制与限流：对回源接口和 Streams 消费做鉴权、限流与审计
- 编码规范：passkey 统一大小写与编码（建议小写十六进制）；避免不同客户端传参差异导致误判

## 配置对齐（Tracker）
- 中间件（示例在 `dist/example_config.yaml` 的 `chihaya.prehooks`）：
  - `passkey approval`：Redis 集合校验 + 可选回源（GET `http_url?passkey=`）；支持本地缓存TTL
  - `traffic push`：Announce 增量事件写入 Redis Streams，字段如上
- 指标服务：`metrics_addr` 暴露 `/metrics` 与 `/debug/pprof/*`

## 运维建议
- 白名单管理：采用“临时集合 + 原子切换”策略做全量刷新，事件驱动做增量更新
- 消息通道：为 Streams 消费构建监控（积压长度、消费速率、失败重试），预留DLQ与回放能力
- 风控策略：结合 `event`、`du/dd/dt`、地址族与端点行为进行异常检测与封禁
