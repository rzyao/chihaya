# Peer 发现问题排查指南

## 问题描述
两个用户都能成功连接 tracker,显示工作正常,一个用户在做种,一个用户在下载,但是两个用户都没有发现对方。

## 常见原因及排查方法

### 1. IP 地址族不匹配 ⭐ (最常见)

**原因**: Chihaya 严格区分 IPv4 和 IPv6 peers。IPv4 客户端只能看到 IPv4 peers,IPv6 客户端只能看到 IPv6 peers。

**排查方法**:
```bash
# 查看 Prometheus 指标,检查 IPv4 和 IPv6 peers 分布
curl http://localhost:6880/metrics | grep -E "chihaya_(seeders|leechers)"

# 检查客户端使用的 IP 版本
# 在客户端 announce 请求中查看 IP 参数
```

**解决方案**:
- 确保两个客户端使用相同的 IP 协议版本(都用 IPv4 或都用 IPv6)
- 如果客户端支持双栈,配置客户端优先使用 IPv4
- 检查 tracker URL 是否使用域名,确保域名解析到相同的 IP 版本

**验证命令**:
```bash
# 使用 curl 模拟 announce,检查返回的 peers
curl "http://localhost:6969/announce?info_hash=%01%02%03%04%05%06%07%08%09%0a%0b%0c%0d%0e%0f%10%11%12%13%14&peer_id=-TEST01-123456789012&port=6881&uploaded=0&downloaded=0&left=1000&event=started&numwant=50&compact=1"
```

---

### 2. InfoHash 不一致

**原因**: 两个用户使用的种子文件 InfoHash 不同,被视为不同的 swarm。

**排查方法**:
```bash
# 在客户端中查看种子的 InfoHash
# qBittorrent: 右键种子 -> 属性 -> 信息哈希
# Transmission: 种子详情 -> Hash

# 或者使用 bencode 工具解析种子文件
# Python 示例:
# import hashlib, bencode
# with open('torrent.torrent', 'rb') as f:
#     info_hash = hashlib.sha1(bencode.bencode(bencode.bdecode(f.read())[b'info'])).hexdigest()
```

**解决方案**:
- 确保两个用户使用完全相同的种子文件
- 不要手动修改种子文件
- 从同一来源下载种子文件

---

### 3. Passkey 路由问题

**原因**: 如果 announce 路由配置为 `/announce/:passkey`,不同的 passkey 可能导致问题。

**排查方法**:
```bash
# 检查配置文件中的路由设置
grep -A 5 "announce_routes:" /usr/local/etc/chihaya/config.yaml

# 检查客户端使用的 announce URL
# 正确格式示例:
# http://tracker.example.com:6969/announce/USER_PASSKEY_HERE
```

**当前配置**:
```yaml
announce_routes:
  - "/announce"
```

如果路由是 `/announce/:passkey`,确保:
- 两个用户的 passkey 都已在 Redis 中注册
- passkey 验证逻辑不会影响 peer 存储

---

### 4. NumWant 参数问题

**原因**: 客户端请求的 peer 数量为 0,或者 tracker 配置限制了返回数量。

**排查方法**:
```bash
# 检查配置
grep -E "(default_numwant|max_numwant)" /usr/local/etc/chihaya/config.yaml
```

**当前配置**:
```yaml
http:
  max_numwant: 100
  default_numwant: 50
udp:
  max_numwant: 100
  default_numwant: 50
```

**解决方案**:
- 在客户端设置中增加"最大连接数"或"最大 peers 数"
- 确保客户端 announce 请求中 `numwant` 参数 > 0

---

### 5. 存储和分片问题

**原因**: Memory 存储的分片逻辑可能导致查询问题。

**排查方法**:
```bash
# 查看 Prometheus 指标
curl http://localhost:6880/metrics | grep -E "chihaya_storage"

# 检查特定 InfoHash 的 peers 数量
curl http://localhost:6969/scrape?info_hash=%01%02%03%04%05%06%07%08%09%0a%0b%0c%0d%0e%0f%10%11%12%13%14
```

**当前配置**:
```yaml
storage:
  name: "memory"
  config:
    shard_count: 1024
    peer_lifetime: "31m"
```

---

### 6. Peer 过期时间问题

**原因**: Peer 在下次 announce 之前就过期了。

**当前配置**:
```yaml
announce_interval: "30m"
peer_lifetime: "31m"
```

这个配置是合理的(peer_lifetime > announce_interval),但如果客户端没有按时 announce,peer 可能会过期。

**排查方法**:
- 检查客户端日志,确认 announce 频率
- 查看 tracker 日志中的 peer 过期记录

---

### 7. 客户端事件状态问题

**原因**: 客户端发送了错误的事件状态。

**Announce 事件类型**:
- `started`: 开始下载
- `completed`: 下载完成
- `stopped`: 停止下载
- (空): 定期更新

**排查方法**:
- 检查做种客户端是否发送了 `event=completed` 或 `event=started`
- 检查下载客户端是否发送了 `event=started`

---

## 调试步骤

### 步骤 1: 启用调试日志
```bash
# 重启 chihaya 并启用调试模式
./bin/chihaya --config /usr/local/etc/chihaya/config.yaml --debug --json
```

### 步骤 2: 监控 Prometheus 指标
```bash
# 持续监控 seeders 和 leechers 数量
watch -n 1 'curl -s http://localhost:6880/metrics | grep -E "chihaya_(seeders|leechers|announces)"'
```

### 步骤 3: 抓包分析
```bash
# 使用 tcpdump 抓取 tracker 流量
tcpdump -i any -w tracker.pcap port 6969

# 使用 Wireshark 分析 announce 请求和响应
```

### 步骤 4: 手动测试 Announce
```bash
# 模拟 Seeder announce
curl -v "http://localhost:6969/announce?info_hash=%12%34%56%78%9a%bc%de%f0%12%34%56%78%9a%bc%de%f0%12%34%56%78&peer_id=-qB4250-SEEDER123456&port=51413&uploaded=1000000&downloaded=0&left=0&event=started&numwant=50&compact=1&ip=192.168.1.100"

# 模拟 Leecher announce
curl -v "http://localhost:6969/announce?info_hash=%12%34%56%78%9a%bc%de%f0%12%34%56%78%9a%bc%de%f0%12%34%56%78&peer_id=-qB4250-LEECH1234567&port=51414&uploaded=0&downloaded=500000&left=500000&event=started&numwant=50&compact=1&ip=192.168.1.101"

# 检查第二次 announce 是否返回了第一个 peer
```

### 步骤 5: 检查 Scrape 响应
```bash
# Scrape 可以查看 swarm 的整体状态
curl "http://localhost:6969/scrape?info_hash=%12%34%56%78%9a%bc%de%f0%12%34%56%78%9a%bc%de%f0%12%34%56%78"

# 响应示例:
# d5:filesd20:<infohash>d8:completei1e10:incompletei1e10:downloadedi0eee
# complete: seeders 数量
# incomplete: leechers 数量
```

---

## 快速诊断检查清单

- [ ] 两个客户端使用相同的 IP 协议版本(都是 IPv4 或都是 IPv6)
- [ ] 两个客户端使用完全相同的种子文件(InfoHash 一致)
- [ ] 两个客户端使用相同的 tracker URL
- [ ] 两个客户端的 passkey 都已验证通过
- [ ] 客户端的 announce 间隔小于 peer_lifetime
- [ ] 客户端请求的 numwant > 0
- [ ] Tracker 日志中没有错误信息
- [ ] Scrape 显示正确的 seeders 和 leechers 数量
- [ ] 防火墙允许客户端之间的连接

---

## 最可能的解决方案

根据经验,**90% 的情况是 IP 地址族不匹配**:

1. **检查客户端 IP 版本**:
   - 在客户端中查看连接的 tracker 地址
   - 如果是 `http://[IPv6地址]:6969/announce`,则使用 IPv6
   - 如果是 `http://IPv4地址:6969/announce`,则使用 IPv4

2. **统一 IP 版本**:
   - 方法 1: 在客户端设置中禁用 IPv6
   - 方法 2: 使用域名作为 tracker 地址,确保 DNS 只返回一种 IP 版本
   - 方法 3: 配置客户端优先使用 IPv4

3. **验证**:
   ```bash
   # 查看 metrics,确认 IPv4 和 IPv6 peers 在同一个 swarm
   curl http://localhost:6880/metrics | grep chihaya_storage_peers_total
   ```

---

## 需要更多帮助?

如果以上方法都无法解决问题,请提供以下信息:

1. 两个客户端的完整 announce URL
2. Tracker 的 Prometheus metrics 输出
3. 客户端的种子 InfoHash
4. Tracker 调试日志(启用 `--debug` 后的输出)
5. Scrape 响应内容
