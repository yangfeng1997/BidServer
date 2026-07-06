# 最终 pprof 瓶颈确认

## 测试背景

- 日期：2026-07-06
- 模式：单机单 RouterAgent，本地 UDS 转发
- 拓扑：`client -> gatesvr -> RouterAgent(UDS) -> lobbysvr`
- 目标：在当前优化版本上做最终 pprof / trace 瓶颈确认，判断继续追单链路极限还是转向多节点横向扩展压测。

## 测试前清理

本轮按以下流程重新执行，避免旧进程和旧 pprof 端口污染结果：

1. 按精确 pid 文件停止当前服务：
   - `run/gatesvr/bin/gatesvr.pid`
   - `run/lobbysvr/bin/lobbysvr.pid`
   - `run/routeragent/bin/routeragent.pid`
2. 扫描并停止残留的 `run/*/bin/{gatesvr,lobbysvr,routeragent}` 进程。
3. 等待 etcd lease TTL 过期，实际等待 `15s`。
4. 使用新的 pprof 端口启动并校验：
   - gatesvr: `127.0.0.1:6261`
   - lobbysvr: `127.0.0.1:6262`
   - routeragent: `127.0.0.1:6263`
5. 用 `/debug/pprof/cmdline` 校验端口确实对应本轮进程。
6. smoke test 通过后再压测采样。

## 压测结果

`1000 clients x 8000 requests`：

```text
sent=8000000
ok=8000000
failed=0
reconnects=0
elapsed_ms=22946
qps=348629.80
p50=2.660ms
p90=3.875ms
p99=6.163ms
max=33.066ms
```

长测前半段一度稳定在 `355k-358k QPS`，后半段受 profile/trace 采样和运行抖动影响，整体均值为 `348.6k QPS`。

RouterAgent metrics：

```text
routeragent_forward_total 16000002
routeragent_route_miss 0
routeragent_unknown_seq 0
routeragent_late_response 0
routeragent_remote_seq_pending 0
routeragent_remote_seq_pending_current 0
routeragent_incoming_peer_seq 0
```

## CPU profile 结论

### gatesvr

CPU 总样本约 `85.33s / 10s`，说明 gate 已经能吃到多核，且是当前主瓶颈。

主要热点：

```text
internal/runtime/syscall/linux.Syscall6     35.86%
runtime.futex                               21.01%
runtime.selectgo                            2.58% flat / 20.65% cum
runtime.lock2                               11.67% cum
runtime.unlock2                             15.87% cum
TCPConn.writeLoop                           23.56% cum
TCPConn.readLoop                            16.43% cum
gate.recvLoop                               16.22% cum
ragent.Client.readLoop                       6.68% cum
```

判断：gate 已经主要卡在网络读写 syscall、channel/select 调度、锁竞争/调度开销，而不是某个业务函数。

### lobbysvr

CPU 总样本约 `12.67s / 10s`，负载明显低于 gate。

主要热点：

```text
internal/runtime/syscall/linux.Syscall6     35.75%
ragent.Client.readLoop                      46.09% cum
proto.MarshalOptions.marshal                 3.47% cum
proto.UnmarshalOptions.unmarshal             4.42% cum
lobby.handleInbound                         20.36% cum
ragent.Client.writeLoop                     10.18% cum
```

判断：lobby 已经不是系统上限，剩余成本主要是 UDS 读写、protobuf 编解码和调度。

### routeragent

CPU 总样本约 `12.51s / 10s`，负载也明显低于 gate。

主要热点：

```text
internal/runtime/syscall/linux.Syscall6     27.82%
runtime.futex                               11.43%
runtime.selectgo                            17.59% cum
UDSConn.readLoop                            27.66% cum
Module.routeFrame                           10.15% cum
Module.handleFrame                          10.39% cum
UDS write syscall                           11.35% cum
```

判断：RouterAgent 在单机单 RA 本地转发场景下不是 CPU 上限；主要是 UDS read/write 和调度成本。

## Heap profile 结论

### gatesvr alloc_space

```text
gate.forwardToBackend              1.02GB cum
GateDispatcher.HandleSessionPacket 1.35GB cum
gate.recvLoop                      0.58GB
rpc.Core.OnResponseWithRelease     0.46GB
codec.EncodeDataMessagePacket      0.45GB
ragent.Client.readLoop             0.90GB cum
TCPConn.readLoop                   0.35GB
rpc.Core.Call                      0.35GB
```

判断：gate 还有分配，但已经分散在请求生命周期、response callback、packet/message 编码、TCP read body 和 RPC pending 上。继续优化需要更大结构调整。

### lobbysvr alloc_space

```text
ragent.Client.readLoop              1114MB cum
lobby.Handler.Ping                  1257MB cum
lobby.dispatchRoute                 2079MB cum
proto.MarshalOptions.marshal         394MB
proto consumeStringValidateUTF8      331MB
ragent.releaseFrameBuffer            220MB
```

判断：lobby 的剩余大头是业务对象/protobuf 编解码和 ragent read 生命周期。Ping/Tong 业务过轻，proto 成本在 profile 中自然显眼。

### routeragent alloc_space

```text
UDSConn.readLoop             1.90GB
Module.handleConn            1.17GB
AppendRPCWireHeader          0.58GB
```

判断：RouterAgent 内部还存在 UDS 收包复制和 header 重编码分配，但之前 owned-frame 试验已验证局部池化/引用计数模型没有稳定收益。要真正降这块，需要重设计 frame 生命周期或把结构化 header 延迟编码贯穿 RA 内部。

## Trace 结论

### gatesvr

- scheduler delay 主要在 `runtime.selectgo`，以及 `conn.SendOwned` / response callback 入队路径。
- sync blocking 中 `runtime.selectgo` 占绝大多数，`TCPConn.writeLoop` 和 `gate.recvLoop` 都在等待 channel / IO。
- syscall blocking：

```text
syscall.write 60.27%
syscall.read  39.73%
TCPConn.writeLoop 55.48% cum
TCPConn.readLoop  29.06% cum
```

判断：gate 已经是网络 IO + channel 调度型瓶颈。

### lobby / routeragent

- lobby trace 中 `ragent.Client.readLoop` 和 `SendRPCFrame` 相关路径可见，但总体延迟远低于 gate。
- RouterAgent trace 中主要是 `UDSConn.readLoop/writeLoop`、`Module.handleConn`、`runtime.selectgo`。

判断：lobby / RouterAgent 不是当前单链路主上限。

## 结论

当前单机单 RA 链路已经接近现有架构下的低风险优化上限。继续追单链路极限，剩余方向都属于结构性改动：

- 减少 gate 侧 goroutine/channel 切换。
- 减少 TCPConn writeLoop 复制和 channel 排队。
- 重构 RouterAgent 内部 frame/header 生命周期，避免 UDS read 复制和 header 重编码。
- 降 protobuf marshal/unmarshal 成本，或换更轻业务协议做极限压测。

这些改动风险和侵入性明显高于前几轮优化，收益也不确定。

建议下一阶段转向多节点横向扩展压测：

1. 保留当前单链路 `350k QPS` 左右作为稳定基线。
2. 做 `N gate + N lobby + N RouterAgent` 分片压测，验证吞吐是否接近线性扩展。
3. 再做本机双 RA / 跨机 RA 对比，确认 TCP peer 链路在多节点下的瓶颈。
4. 如果横向扩展不线性，再针对具体瓶颈回到单模块 profile。
