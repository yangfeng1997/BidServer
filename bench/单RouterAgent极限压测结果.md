# 单 RouterAgent 极限压测结果

## 测试背景

- 日期：2026-07-06
- 模式：单机单 RouterAgent，多 gatesvr / 多 lobbysvr 共享同一个 RouterAgent UDS
- 目标：继续压榨单个 RouterAgent 的聚合上限，找到加 gate 后的吞吐拐点和 RouterAgent 热点。

## 测试前清理

每轮测试前都按以下流程执行：

1. 停止 `bench/single_ra_multi_gate_run/*/bin/*.pid` 记录的服务进程。
2. 二次检查仍存活的 benchmark 服务进程并强制停止。
3. 等待 `15s`，让 etcd lease TTL 过期，避免旧节点注册残留。
4. 重启本轮所需的 RouterAgent / lobby / gate。
5. 用 pprof cmdline 和 client_sim smoke test 校验端口与链路。

未使用 `stopall.sh` / `startall.sh`，未运行配置 bake / dry-run。

## 拓扑

所有业务进程共享同一个 RouterAgent UDS：

```text
/tmp/bidserver_single_ra_multi_gate.sock
```

RouterAgent：

```text
nodeid 99.6.0
pprof 127.0.0.1:7270
```

Gate / Lobby：

```text
gatesvrN: 99.1.N, TCP 127.0.0.1:7000+N, pprof 127.0.0.1:7270+N
lobbysvrN: 99.2.N, pprof 127.0.0.1:7280+N
```

## 结果汇总

| 拓扑 | 压测参数 | 聚合 QPS | 平均 p99 | 最大 p99 | failed | reconnects | 结论 |
|---|---|---:|---:|---:|---:|---:|---|
| 4 gate + 4 lobby + 1 RA | 每 gate `500 clients x 4000 req` | 435692.64 | 10.22ms | 10.35ms | 0 | 0 | 当前峰值 |
| 6 gate + 6 lobby + 1 RA | 每 gate `500 clients x 4000 req` | 386257.58 | 17.00ms | 17.51ms | 0 | 0 | 开始退化 |
| 8 gate + 8 lobby + 1 RA | 每 gate `500 clients x 4000 req` | 344906.51 | 25.99ms | 26.30ms | 0 | 0 | 明显退化 |
| 16 gate + 16 lobby + 1 RA | 每 gate `250 clients x 3000 req` | 282578.48 | 34.57ms | 35.18ms | 0 | 0 | 过载排队 |

对比单 gate 单链路：

```text
单 gate 单 RA: 约 350k QPS，p99 约 6.2ms
4 gate 单 RA: 约 436k QPS，p99 约 10.2ms
6 gate 单 RA: 约 386k QPS，p99 约 17.0ms
8 gate 单 RA: 约 345k QPS，p99 约 26.0ms
16 gate 单 RA: 约 283k QPS，p99 约 34.6ms
```

## RouterAgent profile

8 gate 版本采集 RouterAgent CPU profile：

```text
Duration: 20s
Total samples: 39.96s
CPU: 199.79%，约 2.0 核
```

主要热点：

```text
internal/runtime/syscall/linux.Syscall6  28.65%
runtime.futex                            13.26%
runtime.selectgo                         12.86% cum
runtime.lock2                            11.94% cum
UDSConn.readLoop                         23.97% cum
UDSConn.writeLoop                        14.64% cum
Module.handleConn                        10.66% cum
Module.routeFrame                         8.03% cum
net.(*netFD).Write                       11.11% cum
internal/poll.(*FD).Read                 17.27% cum
```

16 gate 版本采集 RouterAgent CPU profile：

```text
Duration: 20s
Total samples: 42.07s
CPU: 210.32%，约 2.1 核
```

主要热点：

```text
internal/runtime/syscall/linux.Syscall6  30.43%
runtime.futex                            15.71%
runtime.lock2                            13.50% cum
runtime.selectgo                         10.48% cum
UDSConn.readLoop                         24.29% cum
UDSConn.writeLoop                        17.52% cum
Module.handleConn                         8.30% cum
Module.routeFrame                         7.01% cum
syscall.write                            12.84% cum
internal/poll.(*FD).Read                 17.71% cum
```

RouterAgent alloc_space 在高并发下仍高度集中：

```text
UDSConn.readLoop       约 50%
Module.handleConn      约 32%
AppendRPCWireHeader    约 16%
pickTargets            < 1%
```

## 关键观察

1. 单 RouterAgent 的最佳点不是 gate 数越多越好。本机这组测试里，`4 gate + 4 lobby + 1 RA` 达到最高聚合吞吐，约 `436k QPS`。
2. 从 6 gate 开始，RouterAgent 侧的 UDS read/write、runtime lock/futex/select、调度和 syscall 成本开始压过多 gate 带来的并行收益。
3. 8 gate / 16 gate 时 RouterAgent CPU 只有约 `2.0` - `2.1` 核，但吞吐下降、p99 上升，说明瓶颈不是简单 CPU 打满，而是共享 UDS、channel/select、锁竞争、调度与 syscall 阻塞造成的排队。
4. profile 没有出现新的业务函数热点；还是 `UDSConn.readLoop/writeLoop`、`handleConn`、`routeFrame`、`AppendRPCWireHeader` 和 Go runtime 调度/锁/syscall。
5. metrics 全程正常：`route_miss=0`、`unknown_seq=0`、`late_response=0`、`remote_seq_pending=0`，没有路由错误或丢响应。

## 结论

当前单 RouterAgent 在本机本协议下的实测有效上限约为：

```text
约 430k - 440k QPS
```

再继续增加 gate/lobby 数量不会压出更高吞吐，反而会把共享 RouterAgent 的 UDS 与调度路径推入排队区间。

如果继续优化单 RA，剩余空间主要是结构性改造：

1. RouterAgent 内部 frame 生命周期重构，减少 `UDSConn.readLoop` 的每帧分配和复制。
2. RouterAgent 转发路径延迟/合并 header 编码，降低 `AppendRPCWireHeader` 分配。
3. 降低 `handleConn -> BaseApp.Post -> taskqueue.Post -> handleFrame` 的 goroutine/channel/queue 切换。
4. UDS 写路径批量化或更直接的 per-conn 写入策略，降低 write syscall / channel 排队。

这些都不是低风险小优化。以当前数据看，下一阶段更值得做横向扩展压测：`N gate + N lobby + N RouterAgent`，看多 RouterAgent 分片后是否能把 `~436k QPS / RA` 近似线性放大。
