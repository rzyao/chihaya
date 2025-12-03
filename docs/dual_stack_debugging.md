# 双栈 Peers 发现调试指南

## 问题描述
两个下载器依然没有互相发现,需要通过调试日志诊断问题。

## 启动 Tracker 并启用调试日志

```bash
# 启动 tracker,启用调试模式
./bin/chihaya.exe --config dist/config.yaml --debug
```

## 关键调试日志

### 1. AnnouncePeers 调用日志

每次客户端 announce 时,会输出:
```
storage: AnnouncePeers called
  InfoHash: <infohash>
  seeder: true/false
  numWant: 50
  announcer: <peer_id>@[<ip>]:<port>
  announcerFamily: IPv4/IPv6
  enableDualStack: true/false
```

**检查点**:
- ✅ `enableDualStack` 应该是 `true`
- ✅ `announcerFamily` 显示客户端使用的 IP 协议版本
- ✅ `InfoHash` 应该完全相同

### 2. 双栈 Swarm 状态日志

```
storage: dual-stack swarm status
  InfoHash: <infohash>
  sameFamily: IPv4/IPv6
  sameSwarmExists: true/false
  sameSeeders: <数量>
  sameLeechers: <数量>
  otherFamily: IPv6/IPv4
  otherSwarmExists: true/false
  otherSeeders: <数量>
  otherLeechers: <数量>
```

**检查点**:
- ✅ 如果一个客户端是 IPv4,另一个是 IPv6,应该看到:
  - `sameSwarmExists: true` 和 `otherSwarmExists: true`
  - 或者至少其中一个为 true
- ✅ seeders 和 leechers 数量应该正确

### 3. 返回结果日志

```
storage: AnnouncePeers result
  InfoHash: <infohash>
  announcer: <peer_id>@[<ip>]:<port>
  peersReturned: <数量>
  numWant: <剩余>
```

**检查点**:
- ✅ `peersReturned` 应该 > 0(如果有其他 peers)
- ✅ 如果 `peersReturned: 0`,说明没有找到任何 peers

## 常见问题诊断

### 问题 1: enableDualStack 为 false

**症状**: 日志显示 `enableDualStack: false`

**原因**: 配置文件中 `enable_dual_stack_peers` 设置为 false 或未设置

**解决方案**:
```yaml
# 编辑 dist/config.yaml
storage:
  config:
    enable_dual_stack_peers: true  # 确保设置为 true
```

### 问题 2: InfoHash 不一致

**症状**: 两个客户端的 `InfoHash` 不同

**原因**: 使用了不同的种子文件

**解决方案**:
- 确保两个客户端使用完全相同的种子文件
- 在客户端中检查 InfoHash 是否一致

### 问题 3: 只有一个 swarm 存在

**症状**: 
```
sameSwarmExists: true
otherSwarmExists: false
```

**原因**: 两个客户端使用相同的 IP 协议版本

**解决方案**:
- 确认一个客户端使用 IPv4,另一个使用 IPv6
- 检查客户端的网络设置

### 问题 4: 两个 swarm 都不存在

**症状**:
```
storage: swarm does not exist in any shard
```

**原因**: InfoHash 在 tracker 中不存在

**解决方案**:
- 确保至少有一个客户端已经 announce
- 检查 announce 是否成功

### 问题 5: peersReturned 为 0

**症状**: `peersReturned: 0` 但 swarm 中有 peers

**可能原因**:
1. 所有 peers 都是相同类型(都是 seeder 或都是 leecher)
2. 只有自己一个 peer
3. 代码逻辑问题

**调试步骤**:
1. 检查 `sameSeeders`, `sameLeechers`, `otherSeeders`, `otherLeechers` 的值
2. 确认一个是 seeder,一个是 leecher

## 完整测试流程

### 步骤 1: 启动 Tracker

```bash
./bin/chihaya.exe --config dist/config.yaml --debug
```

### 步骤 2: 第一个客户端 Announce (IPv4 Seeder)

使用 curl 模拟:
```bash
curl "http://localhost:6969/announce?info_hash=%12%34%56%78%9a%bc%de%f0%12%34%56%78%9a%bc%de%f0%12%34%56%78&peer_id=-TEST01-IPv4SEEDER01&port=6881&uploaded=1000000&downloaded=0&left=0&event=started&numwant=50&compact=1&ip=192.168.1.100"
```

**预期日志**:
```
storage: AnnouncePeers called
  announcerFamily: IPv4
  enableDualStack: true
  seeder: true

storage: dual-stack swarm status
  sameFamily: IPv4
  sameSwarmExists: true
  sameSeeders: 1
  otherSwarmExists: false
```

### 步骤 3: 第二个客户端 Announce (IPv6 Leecher)

```bash
curl "http://localhost:6969/announce?info_hash=%12%34%56%78%9a%bc%de%f0%12%34%56%78%9a%bc%de%f0%12%34%56%78&peer_id=-TEST02-IPv6LEECH001&port=6882&uploaded=0&downloaded=500000&left=500000&event=started&numwant=50&compact=1&ip=2001:db8::1"
```

**预期日志**:
```
storage: AnnouncePeers called
  announcerFamily: IPv6
  enableDualStack: true
  seeder: false

storage: dual-stack swarm status
  sameFamily: IPv6
  sameSwarmExists: true
  sameSeeders: 0
  sameLeechers: 1
  otherFamily: IPv4
  otherSwarmExists: true
  otherSeeders: 1
  otherLeechers: 0

storage: AnnouncePeers result
  peersReturned: 1  # 应该返回 IPv4 seeder
```

## 如果问题仍然存在

请提供以下信息:

1. **完整的调试日志** (从启动到两次 announce)
2. **两个客户端的 announce URL** (确认 InfoHash 是否一致)
3. **配置文件** (`dist/config.yaml` 的 storage 部分)
4. **客户端类型** (qBittorrent, Transmission, curl 等)

## 快速检查命令

```bash
# 检查配置是否正确
grep -A 5 "enable_dual_stack_peers" dist/config.yaml

# 查看最近的调试日志
# (如果使用 systemd)
journalctl -u chihaya -f --since "5 minutes ago" | grep "storage:"
```
