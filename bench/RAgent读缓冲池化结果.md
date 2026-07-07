# RAgent 读缓冲池化结果

## 测试背景

- 日期：2026-07-06
- 模式：单机单 RouterAgent，本地 UDS 转发
- 拓扑：`client -> gatesvr -> RouterAgent(UDS) -> lobbysvr`
- 本轮目标：验证 `internal/core/ragent` 读帧缓冲池化是否能继续降低分配并抬高 QPS。

## 本轮改动

### 1. ragent client 读帧缓冲池化

`internal/core/ragent/sdk/client.go` 中的 `readFrame` 从每帧 `make([]byte, length)` 改成从 `sync.Pool` 获取缓冲区，并返回 `Frame + buffer`。

释放点按帧生命周期区分：

- 握手 ack：`Connect` 读取并校验后释放。
- RPC response：通过 `rpc.Core.OnResponseWithRelease` 延迟到业务回调执行完成后释放。
- RPC request / notify：延迟到 `poster.Post` 的处理闭包结束后释放。
- heartbeat / 未识别帧：当前 goroutine 立即释放。

### 2. rpc.Core 支持回包资源释放回调

新增 `OnResponseWithRelease(seq, payload, code, release)`，原 `OnResponse` 保持兼容并委托给新方法。

这个改动的关键是保证 response payload 在用户回调使用期间仍然有效，回调结束后再归还底层 buffer。

## 验证

```text
go test ./...
PASS
```

烟雾测试：

```text
ok=1 failed=0 latency=1.234ms
```

## 压测结果

单机单 RA，`1000 clients x 3000 requests`：

| 阶段 | QPS | p50 | p90 | p99 | max | failed |
|---|---:|---:|---:|---:|---:|---:|
| 剩余热路径优化最好结果 | 345894.63 | 2.632ms | 3.857ms | 6.266ms | 102.431ms | 0 |
| ragent 读缓冲池化第 1 轮 | 343183.77 | 2.650ms | 3.900ms | 6.516ms | - | 0 |
| ragent 读缓冲池化第 2 轮 | 346109.54 | - | - | 6.636ms | - | 0 |

## profile 观察

改动后：

- gatesvr 中 `ragent.readFrame` 从约 `18.92%` 降到约 `0.65%`。
- lobbysvr 中 `ragent.readFrame` 约 `0.19%`。
- lobbysvr 仍能看到 `ragent.(*Client).readLoop` 较大持有量，约 `777MB`。
- `releaseFrameBuffer` / `sync.Pool` 也变成可见开销，其中 `releaseFrameBuffer` 约 `183MB`，`sync.(*poolChain).pushHead` 约 `52MB`。

## 结论

- 池化确实明显压低了 `ragent.readFrame` 的直接分配。
- 吞吐没有稳定突破上一轮高点，最好结果 `346.1k QPS` 与上一轮 `345.9k QPS` 基本持平。
- 当前收益主要是分配结构改善，不是吞吐跃升。
- 后续继续做 buffer 生命周期优化时，不能只改业务进程侧 ragent client；RouterAgent 内部 UDS/peer 收包、转发、写出路径也需要 ownership-aware，否则中间环节仍会保留大量跨 goroutine buffer 生命周期。

## RouterAgent 内部 owned frame 试验

继续尝试过 RouterAgent 内部 `UDSConn.readLoop` / `tcpPeerLink.readLoop` 的 owned frame 模型：

- 收包时复制到 pooled buffer，并携带 release owner。
- `handleConn` / `handlePeerFrame` 在路由完成后释放。
- frame 被投递到异步 send channel 后，由写循环编码完成再释放。
- 本地多节点、跨 RA、多 peer pending 队列均按引用计数兼容，没有写死当前单 lobby 场景。

验证结果：

```text
go test ./...
PASS

go test -race ./internal/server/routeragent ./internal/server/gate ./internal/core/ragent ./internal/core/rpc
PASS
```

压测结果：

| 阶段 | QPS | p50 | p90 | p99 | max | failed |
|---|---:|---:|---:|---:|---:|---:|
| RouterAgent owned frame 第 1 轮 | 346259.90 | 2.640ms | 3.818ms | 6.460ms | 18.719ms | 0 |
| RouterAgent owned frame 第 2 轮 | 338520.49 | 2.704ms | 3.949ms | 6.526ms | 101.376ms | 0 |
| RouterAgent owned frame 第 3 轮 | 337826.23 | 2.716ms | 3.946ms | 6.539ms | 123.447ms | 0 |
| 回滚 owned frame 后复测 | 345867.81 | 2.656ms | 3.846ms | 6.323ms | 123.444ms | 0 |

结论：RouterAgent 内部 owned frame 模型测试正确，但吞吐不稳定且没有超过现有稳定版本；引用计数、额外复制和 pool 归还成本抵消了分配收益。因此本试验已回滚，只保留 `internal/core/ragent` 侧读缓冲池化和 `rpc.Core.OnResponseWithRelease`，避免把复杂生命周期模型引入主线。

## 下一步

继续抬上限时，优先方向不再是 RouterAgent 内部 buffer ownership，而是更可能直接影响吞吐的点：

- 降低高频日志和状态统计对压测窗口的扰动。
- 继续优化 `EncodeRPCWireHeader` 的分配，尤其是本地直连回包路径的 header 重编码。
- 检查 gate/lobby 业务回包路径是否还能合并编码或复用临时 buffer。
- 做更长时间、多轮 benchmark，区分真实收益和 2% 左右的运行抖动。
