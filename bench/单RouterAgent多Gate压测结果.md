# 单 RouterAgent 多 Gate 压测结果

## 测试背景

- 日期：2026-07-06
- 模式：单机单 RouterAgent，多 gatesvr / 多 lobbysvr 共享同一个 RouterAgent UDS
- 目标：压单个 RouterAgent 的聚合吞吐上限，验证多个网关进程、每个网关独立连接客户端时，RouterAgent 是否成为瓶颈。

## 拓扑

```text
client_sim x4
  -> gatesvr1 127.0.0.1:7001 -> RouterAgent UDS /tmp/bidserver_single_ra_multi_gate.sock -> lobbysvr1
  -> gatesvr2 127.0.0.1:7002 -> RouterAgent UDS /tmp/bidserver_single_ra_multi_gate.sock -> lobbysvr2
  -> gatesvr3 127.0.0.1:7003 -> RouterAgent UDS /tmp/bidserver_single_ra_multi_gate.sock -> lobbysvr3
  -> gatesvr4 127.0.0.1:7004 -> RouterAgent UDS /tmp/bidserver_single_ra_multi_gate.sock -> lobbysvr4
```

节点：

- RouterAgent: `99.6.0`, pprof `127.0.0.1:7270`
- gatesvr: `99.1.1` - `99.1.4`, TCP `7001` - `7004`, WS `7101` - `7104`
- lobbysvr: `99.2.1` - `99.2.4`

本轮启动前清理旧服务进程，并等待 etcd lease TTL 过期，避免旧节点注册残留影响发现结果。

## 压测参数

每个 gate 启动一个 client_sim：

```text
clients=500
requests=4000
```

总请求量：

```text
4 gates x 500 clients x 4000 requests = 8,000,000 requests
```

## 压测结果

| Gate | QPS | failed | reconnects | p99 |
|---|---:|---:|---:|---:|
| gate1 | 106041.69 | 0 | 0 | 约 10.6ms - 11ms |
| gate2 | 105961.05 | 0 | 0 | 约 10.6ms - 11ms |
| gate3 | 106065.55 | 0 | 0 | 约 10.6ms - 11ms |
| gate4 | 105750.20 | 0 | 0 | 约 10.6ms - 11ms |
| 合计 | 423818.49 | 0 | 0 | 约 10.6ms - 11ms |

对比单 gate 单链路最终 pprof 基线：

```text
单 gate 单 RA: 约 348.6k - 354.6k QPS，p99 约 6.2ms
4 gate 单 RA: 约 423.8k QPS，p99 约 10.6ms - 11ms
```

## RouterAgent profile

RouterAgent CPU profile：

```text
总 CPU 样本约 19s / 10s，约 189.99%，即约 1.9 核。
```

主要热点：

```text
internal/runtime/syscall/linux.Syscall6  26.26%
runtime.futex                            11.53%
runtime.selectgo                         14.58% cum
UDSConn.readLoop                         21.42% cum
UDSConn.writeLoop                        17.42% cum
Module.handleConn                        12.74% cum
Module.routeFrame                        10.16% cum
net.(*netFD).Write                       12.37% cum
syscall.write                            11.74% cum
```

RouterAgent alloc_space：

```text
UDSConn.readLoop       1959MB / 50.77%
Module.handleConn      1230MB / 31.88%
AppendRPCWireHeader     620MB / 16.08%
```

RouterAgent metrics：

```text
routeragent_forward_total 16000008
routeragent_route_miss 0
routeragent_unknown_seq 0
routeragent_late_response 0
routeragent_remote_seq_pending 0
routeragent_incoming_peer_seq 0
```

## 结论

1. 单个 RouterAgent 在 4 gate 并发接入下没有 CPU 打满。RouterAgent 约吃 `1.9` 核，说明本轮 423.8k QPS 还不是 RouterAgent 的硬 CPU 上限。
2. 聚合吞吐从单 gate 约 350k 提升到约 424k，说明多个 gate 可以缓解单 gate 的 TCP / channel / syscall 瓶颈，但 4 倍 gate 只带来约 1.2 倍聚合吞吐，不是线性扩展。
3. p99 从单 gate 约 6.2ms 上升到约 10.6ms - 11ms，说明共享单 RouterAgent UDS 后，RouterAgent 的 UDS read/write、handleConn、routeFrame 和调度成本开始成为聚合路径上的排队点。
4. RouterAgent 分配热点仍集中在 `UDSConn.readLoop`、`handleConn`、`AppendRPCWireHeader`。之前 owned-frame 试验已经验证过局部引用计数模型没有稳定收益；要继续压这块，需要更结构化地改 frame/header 生命周期，而不是再做小修小补。

当前判断：

- 单链路上限主要在 gate。
- 多 gate 聚合后，单 RouterAgent 还没饱和，但已经出现 UDS / 调度 / routeFrame 侧排队迹象。
- 下一步如果要找单 RouterAgent 的真实上限，应继续增加 gate/lobby/client_sim 数量，直到 RouterAgent CPU 接近打满或 p99 明显失控。
- 如果目标是生产形态扩展能力，更值得转向 `N gate + N lobby + N RouterAgent` 分片压测，验证横向扩展是否接近线性。
