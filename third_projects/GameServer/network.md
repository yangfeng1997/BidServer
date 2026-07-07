# 网络与连接层

本文件是 `CLAUDE.md` / [`architecture.md`](architecture.md) 的子文档，描述客户端连接相关的网络层：两层消息帧、握手协议、Agent 连接管理与 Session 数据对象。写网络 / 连接相关代码前先读本文件。变更网络协议、握手流程、Agent / Session 接口时，需同步更新本文件并回看 `architecture.md` 与 `CLAUDE.md` 索引。

> 路由表（`MsgRouteTable` / `ForwardTable`）、Handler 分发、gate 转发完整流程见 [`architecture.md`](architecture.md)；集群 RPC 见 [`cluster.md`](cluster.md)。

---

## 消息协议（两层帧）

**外层 Packet**（`src/framework/network/packet/`）：

```
| type(1) | length(3, big-endian) | body |
```

type：`Handshake(0x01)` / `HandshakeAck(0x02)` / `Heartbeat(0x03)` / `Data(0x04)` / `Kick(0x05)`

**内层 Message**（`src/framework/network/message/`，仅 Data 包携带）：

```
Request:  | type(1) | MID(2) | MsgID(4) | data |
Response: | type(1) | MID(2) | MsgID(4) | code(4) | data |
OneWay:   | type(1) | MsgID(4) | data |
```

- `MID`：客户端生成的请求序列号，用于回包匹配
- `MsgID`：proto option `msg_id` 定义的数字消息 ID，替代字符串 route 在网络传输
- `Code`：**仅 Response 携带**的 4 字节框架错误码（big-endian，0=成功，负数=框架保留错误）。由 `src/framework/errors` 定义（`NotFound -404` / `Timeout -408` / `Internal -500` 等）；handler 返回的 `error` 经 `errors.CodeOf` 映射到此字段。**正数业务错误码不走此字段**，由业务 proto 写进 resp body。Agent 的 `ResponseErr(mid, msgID, code)` 用于回带框架错误码的空 body Response。

> `MsgID` 如何映射到 handler / 转发目标，见 [`architecture.md`](architecture.md) 的「消息路由」；框架错误码完整定义见 `src/framework/errors`。

## 握手协议（`src/framework/network/handshake/`）

3步流程：

```
客户端 → 服务端: Handshake(0x01)    {sys:{version,platform}, user:{...}}
服务端 → 客户端: Handshake(0x01)    {code:200, sys:{heartbeat,serializer}}
客户端 → 服务端: HandshakeAck(0x02)
```

握手失败回 `{code:400}` 再关闭。Agent 状态机：`Init → WaitAck → Working → Closed`。

## Agent（`src/framework/agent/`）

每条客户端连接一个 `connAgent`：

- `Handle()` 启动 read / write 两个 goroutine（read 阻塞在 ReadPacket 不可省，每连接仅 2 个 goroutine）
- 心跳：并入 write goroutine 的 select（ticker），服务端主动推，超过 `2×heartbeatSec` 无活动断连
- `Agent` 接口：`Session()` / `Push(msgID, data)` / `Response(mid, msgID, data)` / `ResponseErr(mid, msgID, code)` / `Close` / `OnClose` / `IsAlive` / `RemoteAddr`
- 每连接持一个可取消的生命周期 ctx：`Close()`（断连 / 写错误 / 心跳超时 / 停机强关，均经 `sync.Once`）取消之；`handleData` 用该 ctx 传给本地 dispatch 与 `forwardFn`，故客户端断连会取消在途下游工作（如 gate 转发的 `CallRaw`）
- `ForwardContext`：gate 转发时传入 forwardFn 的上下文（MsgID/MID/MsgType/Data/RespMsgID/ServerType）

`handleData` 路由逻辑：

1. 查 `msgRouteTable[MsgID]` → 本地 dispatch
2. 查 `forwardTable[MsgID]` → 调 `forwardFn`（默认实现：Request→CallAnyRaw，OneWay→CastAnyRaw）

> `msgRouteTable` / `forwardTable` 由 gen_routes 生成，定义见 [`architecture.md`](architecture.md) 的「消息路由」；`forwardFn` 默认实现与完整转发流程见「gate 转发完整流程」。

## Session（`src/framework/session/`）

纯数据对象：`ID`、`UID`、`data map`、`ip`、`frontendID`。底层用 `syncmap.Map` 消除类型断言。

- `Manager.Bind`：事务语义，自动踢旧连接
- `Manager.Close`：幂等，ID 校验防混淆
- `BindNode(typeName, nodeID)` / `BoundNode(typeName)` — 节点绑定，gate 转发时自动路由到绑定节点：

```go
// 玩家登录时，gate handler 里绑定其所属 lobby
sess.BindNode("lobbysvr", "1.2.1")   // 之后发往 lobbysvr 的消息自动路由到 1.2.1
sess.UnbindNode("lobbysvr")          // 断连时解绑

// 泛型辅助，消除类型断言
score, ok := session.GetTyped[int64](sess, "score")
```

> 节点绑定如何驱动 gate 转发，见 [`architecture.md`](architecture.md) 的「gate 转发完整流程」。
