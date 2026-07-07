# RouterAgent 热路径优化结果

## 测试背景

- 日期：2026-07-06
- 模式：单机单 RouterAgent，本地 UDS 转发
- 拓扑：`client -> gatesvr -> RouterAgent(UDS) -> lobbysvr`
- 优化目标：减少 frame 编解码、ragent 读帧、MemberTable 路由选择中的热路径分配，同时保留多节点路由能力。

## 本次改动

### 1. Frame 写路径复用缓冲区

新增 `routeragent.AppendFrame(dst, frame)`，让写循环把 frame 直接 append 到复用的 `[]byte` buffer 中，避免每帧调用 `EncodeFrame` 都分配完整输出切片。

已应用到：

- `internal/core/ragent/agent/uds_conn.go`
- `internal/core/ragent/agent/peer_conn.go`
- `internal/core/ragent/sdk/client.go`

### 2. ragent 读帧减少一次整帧拷贝

`internal/core/ragent.readFrame` 原先会读 `hdr` + `body`，再拼一个 `4+length` 的临时切片给 `DecodeFrame`。现在直接解析 `type/header/body`，少掉一次整帧分配和 copy。

### 3. MemberTable 保留多节点能力的热路径优化

没有把当前“只有一个 lobby”写死。优化方式是：

- `MemberTable.Upsert` 时维护每个 `serverType` 下的节点列表按 `NodeID` 有序。
- 新增 `PickAnyByServerType`、`PickHashByServerType`、`ListNodeIDsByServerType`。
- hash 路由直接在有序列表上取模，不再每个请求 `sort.Slice`。
- `len(items)==1` 是通用快速路径，多节点时仍按稳定有序列表选择。
- 广播仍返回 nodeID 快照，避免外部修改内部 slice。

## 验证

```text
go test ./internal/server/routeragent ./internal/core/ragent ./internal/server/gate ./internal/server/lobby ./tools/client_sim
PASS

go test ./...
PASS
```

烟雾测试：

```text
ok=1 failed=0 latency=584.305us
```

## 压测结果

优化前长压测基线：

| clients | requests/client | QPS | p50 | p90 | p99 | failed |
|---:|---:|---:|---:|---:|---:|---:|
| 1000 | 3000 | 296187.23 | 2.958ms | 5.006ms | 7.934ms | 0 |

优化后长压测两轮：

| 轮次 | clients | requests/client | QPS | p50 | p90 | p99 | max | failed |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 1000 | 3000 | 303884.11 | 2.925ms | 4.721ms | 7.883ms | 19.044ms | 0 |
| 2 | 1000 | 3000 | 260516.06 | 3.394ms | 5.534ms | 9.158ms | 155.761ms | 0 |

结论：当前容器调度波动很明显，单次 QPS 不足以证明稳定提升。第 1 轮略高于基线，第 2 轮低于基线且 max 延迟明显抖动，说明吞吐受运行时调度 / 容器 CPU 抖动影响较大。

## 分配变化

在 profile 样本口径不完全相同的前提下，方向是明确的：

- `routeragent.EncodeFrame` 不再出现在 RouterAgent heap top 中。
- `MemberTable.ListByServerType` / `Resolver.PickHash` 基本从 RouterAgent 分配热点中消失。
- RouterAgent 剩余主要分配集中在：
  - `UDSConn.readLoop`
  - `Module.handleConn`
  - `EncodeRPCWireHeader`
  - `DecodeRPCWireHeader`

优化后 RouterAgent heap top：

```text
UDSConn.readLoop              43.47%
Module.handleConn             27.19%
DecodeRPCWireHeader           14.46%
EncodeRPCWireHeader           13.90%
Module.pickTargets             0.72%
```

这说明本轮改动确实打掉了 frame 写路径分配和多节点选择里的无谓分配，但读路径和 RPC wire header 仍是主要热点。

## 判断

这轮优化是有效的，但主要收益体现在分配结构更干净，而不是稳定吞吐显著提升。原因是当前单 RA 模式下 RouterAgent 本体原本就不是第一瓶颈，gate TCP 接入侧和运行时调度仍然占主导。

下一步如果继续抬上限，优先级应调整为：

1. 优化 `UDSConn.readLoop`：当前读到共享 buffer 后还会通过 `poster.Post` 异步处理，必须谨慎处理生命周期。可以改成直接按帧复制最小必要数据，或者引入 per-connection frame buffer pool，但要避免 frame 引用被后续 read 覆盖。
2. 优化 `EncodeRPCWireHeader` / `DecodeRPCWireHeader`：减少 string 转换和每次 header 切片分配。
3. 优化 gate TCP 侧：`TCPConn.Send`、`readLoop`、`writeLoop` 和 codec encode/decode 是真正影响单 RA 上限的主路径。
4. 用固定 CPU quota / 绑核 / 多轮中位数复测，避免容器调度抖动把优化收益淹没。
