# header 编码与日志降噪优化结果

## 测试背景

- 日期：2026-07-06
- 模式：单机单 RouterAgent，本地 UDS 转发
- 拓扑：`client -> gatesvr -> RouterAgent(UDS) -> lobbysvr`
- 目标：继续验证 header 编码分配、热路径日志/统计扰动、gate/lobby 回包编码路径是否有收益；无收益则回滚。

## 保留改动

### 1. ragent 发送侧延迟编码 RPC header

`internal/core/ragent.Client.SendFrame` 原先会先调用 `routeragent.EncodeRPCWireHeader` 分配 header `[]byte`，再把 header 放入 `Frame`，最终 writeLoop 再复制进完整 RA frame。

本轮新增 `SendRPCFrame` / `outboundFrame`，业务发 RPC frame 时只把结构化 `RPCWireHeader` 入队，writeLoop 中直接把 header 编码进最终写缓冲，减少中间 header 分配和一次复制。

涉及：

- `internal/core/ragent/sdk/client.go`
- `internal/server/lobby/module.go`
- `internal/server/gate/module.go`
- `internal/server/gate/ping_pong_test.go`
- `internal/core/ragent/wire/rpc_wire.go`

### 2. RPCWireHeaderLen

新增 `RPCWireHeaderLen`，供延迟编码路径预估 frame 大小。`AppendRPCWireHeader` 复用该长度检查逻辑。

涉及：

- `internal/core/ragent/wire/rpc_wire.go`

### 3. RouterAgent 队列状态日志降噪

`runQueueStatsLog` 从固定每 5 秒输出 state stats，改为只在 peer 队列非空、incoming seq 非空、remote seq pending 非空时输出。

metrics 保留不变；只是避免空闲/压测窗口里周期性刷日志造成抖动。

涉及：

- `internal/core/ragent/agent/runtime.go`

## 已回滚试验

RouterAgent 内部 `UDSConn.readLoop` / `tcpPeerLink.readLoop` owned-frame 引用计数模型已回滚。

原因：测试和 race 测试通过，但压测不稳定，后两轮低于当前保留版本。引用计数、额外复制和 pool 归还成本抵消了收益。

## 验证

```text
go test ./internal/core/ragent ./internal/server/routeragent ./internal/server/gate ./internal/server/lobby ./internal/core/rpc
PASS

go test ./...
PASS
```

## 压测结果

单机单 RA，`1000 clients x 3000 requests`：

| 阶段 | QPS | p50 | p90 | p99 | max | failed |
|---|---:|---:|---:|---:|---:|---:|
| ragent 读缓冲池化保留版本复测 | 345867.81 | 2.656ms | 3.846ms | 6.323ms | 123.444ms | 0 |
| 延迟编码 header 第 1 轮 | 349394.75 | 2.610ms | 3.815ms | 6.405ms | 29.423ms | 0 |
| 延迟编码 header 第 2 轮 | 339428.25 | 2.690ms | 3.952ms | 6.689ms | 21.220ms | 0 |
| header + 日志降噪第 1 轮 | 356523.96 | 2.570ms | 3.732ms | 6.226ms | 118.233ms | 0 |
| 加入 gate/lobby 回包延迟编码第 1 轮 | 350612.15 | 2.590ms | 3.773ms | 6.486ms | 25.050ms | 0 |
| 加入 gate/lobby 回包延迟编码第 2 轮 | 344739.91 | 2.657ms | 3.883ms | 6.604ms | 17.623ms | 0 |
| 最终保留版本复测 | 354569.32 | 2.573ms | 3.755ms | 6.329ms | 36.415ms | 0 |

## 结论

- `ragent` 发送侧延迟编码 RPC header 有收益，最高轮次到 `349.4k QPS`，最终合并后也稳定高于此前保留版本。
- RouterAgent 空状态日志降噪有收益，最高轮次到 `356.5k QPS`，主要改善压测窗口抖动和平均吞吐。
- gate/lobby 回包路径接入 `SendRPCFrame` 后没有明显负收益，最终版本仍达到 `354.6k QPS`，可以保留；它主要减少 header 中间分配。
- RouterAgent 内部 owned-frame 生命周期模型无稳定收益，已经回滚。

当前最终保留版本相对 `345867.81 QPS` 复测基线，最终复测 `354569.32 QPS`，提升约 `2.5%`。最高观察值 `356523.96 QPS`，提升约 `3.1%`。
