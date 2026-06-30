# 开发指南（命令与代码放置）

本文件是 `CLAUDE.md` 的子文档，汇总常用命令、工程目录布局、新增代码的放置规则，以及新增 protobuf 协议 / 配置的步骤。新增代码前先读本文件对齐风格。新增/删除/移动目录或包、变更构建与测试命令、调整配置结构时，需同步更新本文件并回看 `CLAUDE.md` 索引。

## 常用命令

```bash
# 构建
go build ./...

# 运行所有测试
go test ./...

# 详细输出
go test -v ./...

# 代码检查（需安装 golangci-lint）
golangci-lint run ./...

# 生成 protobuf 代码（cluster.proto 等）
# 注意：系统 protoc 未安装；借用开发目录下的 protoc
PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc
PROTO_INCLUDE=/game/dev/silver-server/3rd/protobuf/include
$PROTOC -I. -I$PROTO_INCLUDE --go_out=. --go_opt=module=project protocal/<name>.proto

# 生成路由表（扫描所有 proto 生成 protocal/gen/routes/routes.go）
go run ./tools/gen_routes

# 生成配置 struct（读 protoc 输出的 descriptor，输出 conf/schema/gen/）
# 需先运行 protoc 生成 config.pb.descriptor，见「配置相关命令」
go run ./tools/gen_config --pb=conf/schema/gen/config.pb.descriptor --out=conf/schema/gen

# 烘焙服务配置（conf/ 模板 + values → run/{svc}/conf/config.yaml）
go run ./tools/config_build --env=dev --svc=gatesvr
```

> **JetStream 相关代码**：沙箱无 NATS/JetStream 运行环境，单测以 `MemoryQueue` fake 替代；`JetStreamQueue`（`src/common/matchqueue/jetstream.go`）只做编译验证（`go build ./...` / `go vet ./...`），不实跑。

---

## 工程目录与代码放置指南

```
Project/
├── bin/          # go build 产物，不提交
├── conf/         # 配置模板（common.yaml + svc.yaml）+ envs/{env}.yaml + schema/
├── run/          # config_build 产物（不进 git），每个服务 run/{svc}/conf/config.yaml
├── protocal/     # .proto 源文件；gen/ 为生成代码，勿手动修改
├── src/
│   ├── common/   # 通用基础库
│   ├── framework/# 框架层
│   └── servers/  # 各独立服务进程
└── tools/        # 开发期工具（gen_routes / gen_config / config_build；luban/ 为预留空占位，暂未使用）
```

### 新增代码放置规则

| 需求 | 目录 |
|---|---|
| 新通用库（定时器、工具函数等） | `src/common/<能力名>/` |
| 新序列化格式 | `src/common/serialize/<格式名>/` |
| 新 Acceptor 协议 | `src/framework/network/acceptor/xxx_acceptor.go` |
| 新框架能力 | `src/framework/<模块名>/` |
| 新服务进程 | `src/servers/<名>svr/main.go` + `internal/` |
| 服务进程启动封装（cobra CLI/daemon/pid） | `src/framework/cli/`（项目约定层）<br>`src/common/pidfile/`（pid 文件工具）<br>`src/common/daemon/`（进程后台化） |

**已有通用工具**：
- `src/common/mongo` — MongoDB 接入层，异步 CRUD；回调经 `dispatcher`（`taskqueue.Dispatcher`）投递回主循环，避免跨 goroutine 竞态。
- `src/common/syncmap` — 泛型 `sync.Map` 封装，消除类型断言（`Map[K, V]`）
- `src/common/jumphash` — Jump Consistent Hash（`Jump`/`Pick`），无环 / 无虚拟节点 / 零额外内存；`Pick(members, key)` 把成员升序去重后选一个，供 routersvr 解 `CONSISTENT_HASH` 选分片（不同 router 实例对同一 key 结果一致）。
- `src/common/event` — 进程内同步事件总线，泛型类型安全，支持取消订阅：

```go
bus := event.NewBus[PlayerLoginEvent]()
token := bus.Subscribe(func(e PlayerLoginEvent) { ... })
bus.Publish(PlayerLoginEvent{UID: 10001})
bus.Unsubscribe(token)
```

非协程安全，适合单 goroutine（如帧驱动主循环）内使用。

- `src/common/timewheel` — 单层哈希时间轮，O(1) 添加/取消/推进，统一管理海量定时任务（buff/CD/超时/倒计时），避免每个定时各占一个 runtime timer：

```go
tw := timewheel.New(100*time.Millisecond, 512)  // tick × slots，单圈约 51.2s

// 帧驱动服务：每帧推进一格，回调在主循环 goroutine 串行执行（零锁，与 taskqueue 一致）
func (rt *Runtime) tick() { tw.Advance() } // 如 roomsvr 主循环
timer := tw.AfterFunc(3*time.Second, func() { ... })  // 一次性
tw.Tick(1*time.Second, func() { ... })                 // 周期
tw.Stop(timer)                                          // 取消

// 非帧驱动服务：自驱动，内部 goroutine 按 tick 推进（回调线程安全由业务保证）
tw.Start(); defer tw.Close()
```

并发安全；`Advance` 先释放锁再执行回调，回调内可安全再次 AfterFunc/Stop。
（`-race` 检测需在装有 C 编译器的环境运行，如 CI/Linux。）

- `src/common/pidfile` — PID 文件工具（Write/Read/Remove/IsRunning）
- `src/common/daemon` — 进程后台化（Daemonize/FilterArgs，重 exec 自身；Windows 上 Daemonize 返回错误）

每个服务进程建议结构：

```
src/servers/gatesvr/        ← 已有完整样板，可直接参考
├── main.go                 # 入口：Builder 创建 Application，注册 Module 和 Handler
└── internal/
    ├── gate_module.go     # 业务生命周期（Init/OnAfterInit/OnBeforeStop/OnStop）
    └── gate_handler.go     # 本地消息处理（登录/心跳/退出）
```

**Module 生命周期模式**（以 gatesvr 为例）：
```go
func (g *GateModule) Init() {
    // 初始化资源，创建 taskqueue.Queue，构建注入 cluster/taskqueue 的 ctx
    g.tq = taskqueue.New(512)
    g.ctx = cluster.WithCluster(context.Background(), g.cls)
    g.ctx = cluster.WithDispatch(g.ctx, g.tq)
}
func (g *GateModule) OnAfterInit() {
    // 所有模块 Init 完后注册回调（避免初始化竞争）
    g.sessions.OnClose(func(s *session.Session) { ... })
}
func (g *GateModule) OnBeforeStop() { /* 停止接受新请求 */ }
func (g *GateModule) OnStop()       { /* 释放资源，保存数据 */ }
```

### 新增 protobuf 协议

1. 在 `protocal/` 下新增 `.proto`，加 `msg_id` / `server_type` option
2. 借用 protoc（见「常用命令」说明）：
   ```bash
   PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc
   PROTO_INCLUDE=/game/dev/silver-server/3rd/protobuf/include
   $PROTOC -I. -I$PROTO_INCLUDE --go_out=. --go_opt=module=project protocal/<name>.proto
   ```
3. `go run ./tools/gen_routes` 重新生成路由表
4. 勿手动修改 `protocal/gen/` 下的文件

**P4a 新增 proto**：

| 文件 | 主要消息 |
|---|---|
| `protocal/match.proto` | `MatchRequest`（JetStream payload）、`RPC_PublishMatch_Rsp`、`RPC_GameStarted_Req/Rsp` |
| `protocal/room.proto` | `RPC_OpenGame_Req/Rsp`、`Participant` |
| `protocal/lobby.proto`（扩展） | `CS_StartMatch`(2034)、`SC_StartMatch`(2035)、`SC_MatchFound`(2036) |
| `protocal/online.proto`（扩展） | `RPC_BindRoom_Req/Rsp`、`RPC_UnbindRoom_Req/Rsp`；`OnlineEntry` 补 `room_node_id`/`game_id` 字段 |

**P4b 新增 proto**（客户端 msg_id 续段 2037–2041）：

| 文件 | 主要消息 |
|---|---|
| `protocal/lobby.proto`（扩展） | `CS_Bid`(2037)、`SC_Bid`(2038)、`SC_AuctionState`(2039)、`SC_AuctionResult`(2040)、`SC_MatchTimeout`(2041) |
| `protocal/room.proto`（扩展） | `RPC_Bid_Req/Rsp`、`RPC_AuctionState_Notify`、`RPC_Settle_Req/Rsp` |
| `protocal/match.proto`（扩展） | `RPC_MatchTimeout_Notify` |

**P4c 新增 proto**（客户端 msg_id 2042）：

| 文件 | 主要消息 |
|---|---|
| `protocal/lobby.proto`（扩展） | `SC_ReconnectAuction`(2042)（push-only，重连成功时推竞拍快照） |
| `protocal/room.proto`（扩展） | `RPC_Rejoin_Req/Rsp`、`RPC_QueryGame_Req/Rsp`（服务间 RPC，无 msg_id） |

**P4b 新增 MongoDB collection**：

| collection | 说明 |
|---|---|
| `offline_messages` | 离线状态变更消息（竞拍结算/超时回告）；每玩家一 doc，多写者 `$push`；登录链重放后 `$pull`，重放先于放行 |

### 配置相关命令

```bash
# 1. 修改 proto schema 后重新生成 struct + 三张表
protoc --proto_path=. \
       --descriptor_set_out=conf/schema/gen/config.pb.descriptor \
       --include_imports \
       conf/schema/options.proto \
       conf/schema/types.proto \
       conf/schema/common.proto \
       conf/schema/gatesvr.proto   # 按需列出各服务 proto
go run ./tools/gen_config --pb=conf/schema/gen/config.pb.descriptor --out=conf/schema/gen

# 2. 烘焙单个服务配置（dev 环境）
go run ./tools/config_build --env=dev --svc=gatesvr

# 3. 烘焙全部服务（含 common）
go run ./tools/config_build --env=dev --all

# 4. 增量烘焙（仅重建 mtime 变化的服务）
go run ./tools/config_build --env=prod --all --incremental
```

### 新增配置字段步骤

1. 在 `conf/schema/common.proto`（通用字段）或 `conf/schema/<svc>.proto`（服务私有字段）中添加字段，按需加 option 标记：
   - `[(conf.reload) = true]`——可热更（纯读取即生效）
   - `[(conf.env) = true]`——运行时由 `${UPPER_VAR}` 环境变量注入（字段必须为 string）
   - `[(conf.required) = true]`——必填，缺失或零值 → 启动报错
2. 运行「配置相关命令」第 1 步重新生成 struct 和三张表（`conf/schema/gen/`）。
3. 在 `conf/common.yaml` 或对应 `conf/<svc>.yaml` 模板中添加对应 yaml key（字面量或 `${placeholder}`）。
4. 在 `conf/envs/dev.yaml`（以及 prod.yaml）中为小写占位符提供取值；敏感字段写 `${UPPER_VAR}` 让部署平台注入。
5. 运行「配置相关命令」第 2/3 步烘焙产物到 `run/{svc}/conf/config.yaml`。

**注意**：
- `run/` 不进 git（已在 .gitignore）；每次换环境或改 schema 后需重新烘焙。
- 本地开发需设置必填的环境变量（如 `MONGO_URI`、`REDIS_PWD`），否则服务启动报错（这是设计意图：密钥不写明文）。

---

## serverTypeID 分配表

NodeID 格式 `worldID.serverTypeID.serverIndex`，当前单 world MVP 所有服务部署在 world 1（worldID=0 保留给未来跨 world 全局服务，未使用）；serverTypeID 3/4 原 world/scene，现已释放保留：

| serverTypeID | 服务名 | 说明 |
|---|---|---|
| 1 | gatesvr | 网关（前端节点，客户端 TCP/WS 接入） |
| 2 | lobbysvr | 大厅（EC 实体，MongoDB 持久化） |
| 3 | —（已释放） | 原 world/scene 预留，现释放 |
| 4 | —（已释放） | 原 world/scene 预留，现释放 |
| 5 | onlinesvr | 在线目录（纯内存，一致性哈希分片） |
| 6 | routersvr | 路由代理（forward + publishmatch） |
| 7 | roomsvr | 对局房间（P4a，CONSISTENT_HASH by gameId） |
| 8 | matchsvr | 匹配服（P4a，JetStream 消费，off-loop 编排） |

---

## 构建与运行各服务

roomsvr / matchsvr 走新配置体系：`conf/roomsvr.yaml` / `conf/matchsvr.yaml` + values + 烘焙（同其他服务）。

```bash
# 构建单个服务
go build ./src/servers/roomsvr/...
go build ./src/servers/matchsvr/...

# 运行（需先启动 etcd + NATS；先烘焙再运行）
go run ./tools/config_build --env=dev --svc=roomsvr   # 烘焙到 run/roomsvr/conf/config.yaml
go run ./src/servers/roomsvr                          # main 读 run/roomsvr/conf/config.yaml

go run ./tools/config_build --env=dev --svc=matchsvr  # 烘焙到 run/matchsvr/conf/config.yaml
go run ./src/servers/matchsvr                         # main 读 run/matchsvr/conf/config.yaml

# 仅本服务单测（无需 NATS 实例，matchqueue 用 MemoryQueue fake）
go test ./src/servers/roomsvr/...
go test ./src/servers/matchsvr/...
go test ./src/common/matchqueue/...
```
