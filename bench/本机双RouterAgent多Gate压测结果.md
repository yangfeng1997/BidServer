# 本机双 RouterAgent 多 Gate 压测结果

## 测试背景

- 日期：2026-07-06
- 模式：本机双 RouterAgent 模拟跨机器转发，多 gatesvr 连接 RA1，多 lobbysvr 连接 RA2
- 目标：压测 `gate -> RA1(UDS) -> RA1/RA2(TCP loopback) -> RA2(UDS) -> lobby` 跨 RouterAgent 链路在多 gate 下的聚合性能。

## 测试前清理

每轮启动前：

1. 按 `bench/local_cross_multi_gate_run/*/bin/*.pid` 精确停止上轮 benchmark 服务进程。
2. 二次检查残留 benchmark 服务并停止。
3. 等待 `15s`，让 etcd lease TTL 过期。
4. 启动本轮 RA / lobby / gate。
5. 校验 pprof cmdline，并对每个 gate 执行 1 次 smoke ping/tong。

未使用 `stopall.sh` / `startall.sh`，未运行配置 bake / dry-run。

## 拓扑

```text
client_sim x N
  -> gatesvr1..N -> RA1 UDS /tmp/bidserver_cross_ra1.sock
  -> RA1 TCP 127.0.0.1:7300 <-> RA2 TCP 127.0.0.1:7301
  -> RA2 UDS /tmp/bidserver_cross_ra2.sock -> lobbysvr1..N
```

节点：

- RA1: `99.6.1`, listen `127.0.0.1:7300`, pprof `127.0.0.1:7361`
- RA2: `99.6.2`, listen `127.0.0.1:7301`, pprof `127.0.0.1:7362`
- gatesvrN: `99.1.N`, TCP `127.0.0.1:7400+N`, pprof `127.0.0.1:7370+N`
- lobbysvrN: `99.2.N`, pprof `127.0.0.1:7380+N`

## 结果汇总

每个 gate 使用：

```text
clients=500
requests=4000
```

| 拓扑 | 总请求量 | 聚合 QPS | 平均 p99 | 最大 p99 | failed | reconnects | 结论 |
|---|---:|---:|---:|---:|---:|---:|---|
| 2 gate + 2 lobby + 2 RA | 4,000,000 | 336569.94 | 6.26ms | 6.30ms | 0 | 0 | 压力偏低，延迟最好 |
| 4 gate + 4 lobby + 2 RA | 8,000,000 | 366454.82 | 10.71ms | 10.73ms | 0 | 0 | 本轮最高吞吐 |
| 6 gate + 6 lobby + 2 RA | 12,000,000 | 353551.69 | 15.73ms | 15.89ms | 0 | 0 | 开始退化 |
| 8 gate + 8 lobby + 2 RA | 16,000,000 | 339025.21 | 20.83ms | 21.09ms | 0 | 0 | 继续退化 |

对比：

```text
本机单 RA 本地转发最佳：约 435.7k QPS，p99 约 10.2ms
本机双 RA 跨 RA 转发最佳：约 366.5k QPS，p99 约 10.7ms
```

跨 RA TCP loopback 链路相对本地单 RA UDS 转发，峰值吞吐约下降 `16%` 左右。

## Metrics 观察

各轮均为：

```text
route_miss=0
unknown_seq=0
late_response=0
remote_seq_pending=0
remote_seq_pending_current=0
```

peer 队列水位随并发上升：

| 拓扑 | RA1 peer send_max | RA2 peer prio_max |
|---|---:|---:|
| 2 gate | 893 | 1000 |
| 4 gate | 1623 | 2000 |
| 6 gate | 2999 | 2777 |
| 8 gate | 3034 | 3011 |

说明请求方向 RA1 -> RA2 的普通 peer send queue，以及响应方向 RA2 -> RA1 的 priority queue 都出现明显排队，但没有溢出或丢包。

## RouterAgent profile

8 gate 档采集 CPU profile。

RA1：

```text
Total samples: 35.92s / 20s = 179.59%
```

主要热点：

```text
syscall/linux.Syscall6        27.39%
runtime.selectgo              13.25% cum
UDSConn.readLoop              15.79% cum
Module.routeFrame             14.98% cum
Module.forwardRPC             14.28% cum
tcpPeerLink.writeBatch        10.13% cum
tcpPeerLink.readLoop           4.29% cum
AppendRPCWireHeader            2.17% cum
RemoteSeqMap.Alloc             2.12% cum
fmt.Sprintf / peerKey          visible
```

RA2：

```text
Total samples: 27.02s / 20s = 135.10%
```

主要热点：

```text
syscall/linux.Syscall6        30.42%
runtime.selectgo              15.47% cum
UDSConn.writeLoop             14.03% cum
tcpPeerLink.writeBatch         9.84% cum
tcpPeerLink.readLoop           8.40% cum
Module.sendResponseViaPeer     6.81% cum
Module.handlePeerFrame         7.07% cum
UDSConn.readLoop              12.69% cum
```

alloc_space：

RA1：

```text
AppendRPCWireHeader           5991MB / 23.78%
tcpPeerLink.readLoop          5154MB / 20.46% flat, 8879MB cum
UDSConn.readLoop              4756MB / 18.88%
startPeerLoops.func1.1        3724MB / 14.78%
Module.handleConn             3014MB / 11.96%
peerKey / fmt.Sprintf         visible
RemoteSeqMap.Alloc            635MB / 2.52%
pickTargets                   575MB / 2.28%
```

RA2：

```text
tcpPeerLink.readLoop          4698MB / 29.20% flat, 8362MB cum
UDSConn.readLoop              4618MB / 28.70%
handleIncomingPeer.func1      3661MB / 22.75%
Module.handleConn             3073MB / 19.10%
```

## 结论

本机双 RA 跨 RouterAgent 路径的有效上限约为：

```text
约 360k - 370k QPS
```

在 4 gate 时达到峰值，继续加到 6 / 8 gate 后吞吐下降、p99 上升，原因是 peer TCP link、remote seq、priority response queue、UDS 读写和 runtime 调度开始形成排队。

相对单 RA 本地转发峰值 `~436k QPS`，跨 RA loopback 峰值 `~366k QPS` 低约 `16%`，这个差距基本来自多一次 RA peer TCP 转发与远端 seq/response 路径。

下一步优化方向如果继续压跨 RA：

1. 支持同一对 RouterAgent 之间多 peer 连接，并验证 `1/2/4/8` 连接是否能降低 peer queue 水位。
2. 优化 `peerKey` / metrics label 生成，避免热路径 `fmt.Sprintf` / string 分配。
3. 继续减少跨 RA 路径的 header 重编码分配，尤其是 `AppendRPCWireHeader`。
4. 降低 `RemoteSeqMap.Alloc` 和 incoming peer seq 跟踪的分配与锁成本。

当前从容量角度看，跨 RA 单连接本机 loopback 也已经有 `~36万 QPS`，生产规划仍建议按更保守的 `15万-25万 QPS / RA pair` 留余量，真实跨机器网卡和 RTT 需要单独验证。
