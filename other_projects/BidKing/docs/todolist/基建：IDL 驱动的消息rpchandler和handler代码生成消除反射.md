## IDL 驱动的 handler 代码生成（替代运行时反射）

**现状**：`RegisterHandler` 运行时反射扫描方法签名，匹配 handler。问题：

- 签名错误、漏实现方法只能运行时发现
- 反射调用 `method.Call` 有性能开销
- msg_id → 方法绑定靠路由表 + 反射匹配，多一层

**目标**：参考网易 / pomelo 的 IDL First + 代码生成方案，从 proto 生成 handler 接口和注册代码，编译期类型安全 + 运行时零反射。

### 方案

**1. proto 定义（已定：用标准 protobuf service）**

IDL 用 protobuf 格式，采用 **标准 `service` 定义**（gRPC 风格），而非 message option 推导。

```protobuf
// world.proto
syntax = "proto3";

message CS_ClaimReward_Req { int32 reward_id = 1; }
message SC_ClaimReward_Rsp { int32 code = 1; }
message CS_SyncPos_OneWay  { float x = 1; float y = 2; }
message Empty {}

service WorldService {
    rpc ClaimReward(CS_ClaimReward_Req) returns (SC_ClaimReward_Rsp);
    rpc SyncPos(CS_SyncPos_OneWay) returns (Empty);  // returns (Empty) 约定为 OneWay，工具不生成回包
}
```

**定此方案的理由**：

- protobuf 原生支持 `service` 关键字，protoc 可直接解析（用 protoc 插件或读 FileDescriptor），不用自己写 proto 解析器
- 请求/响应配对在 service 里**显式声明**，不靠 Req/Rsp 命名约定推导，更清晰
- 行业通用范式（gRPC/Thrift 同款），新人易懂

**与现有 msg_id option 的关系**：

- msg_id 仍通过 message option 标注（gate 转发路由、网络传输都需要数字 ID）
- service 负责"方法 ↔ 请求/响应类型"的绑定，option 负责"消息 ↔ msg_id"的绑定，两者互补
- OneWay 约定：`returns (Empty)` 表示无回包，生成的接口方法签名为 `func(ctx, req) error`（无 resp 返回值）

---

**选型 B（不推荐）：纯 message option，按 Req/Rsp 命名配对推导**

不用 `service`，每个 message 用 option 标注 msg_id + handler_method，工具按命名约定（`Xxx_Req`/`Xxx_Rsp` 配对）推导接口：

```protobuf
message CS_ClaimReward_Req {
    option (options.msg_id)         = 3001;
    option (options.server_type)    = "worldsvr";
    option (options.handler_method) = "WorldHandler.ClaimReward";
    int32 reward_id = 1;
}
message SC_ClaimReward_Rsp {
    option (options.msg_id) = 3002;
    int32 code = 1;
}
```

**为何不推荐**：

- 请求/响应靠**命名约定**配对（`_Req`/`_Rsp` 后缀），命名不规范就配错，不如 service 显式声明可靠
- 需要自己解析 message option 推导方法签名，比 protoc 原生解析 service 复杂
- OneWay 靠"有 Req 无对应 Rsp"推导，语义模糊
- 唯一优点是复用现有 gen_routes 基础，但这点便利不足以抵消上述缺陷

保留此选型仅作记录：若未来不想引入 service 定义、想纯靠 message option 驱动，可回退到此方案。

**2. 生成接口（业务实现）**

```go
type WorldServiceHandler interface {
    ClaimReward(ctx context.Context, req *CS_ClaimReward_Req) (*SC_ClaimReward_Rsp, error)
    SyncPos(ctx context.Context, req *CS_SyncPos_OneWay) error
}
```

**3. 生成注册函数（直接绑定，无反射）**

```go
func RegisterWorldService(app App, impl WorldServiceHandler) {
    app.bind(3001, func(ctx, data) ([]byte, error) {
        req := &CS_ClaimReward_Req{}
        proto.Unmarshal(data, req)
        rsp, err := impl.ClaimReward(ctx, req)
        ...
    })
    app.bind(3003, ...)
}
```

**4. 业务层用法**

```go
gen.RegisterWorldService(app, &WorldHandlerImpl{})  // 一行，编译期类型安全
```

### 收益对比

|             | 当前（反射）      | IDL 生成       |
| ----------- | ----------------- | -------------- |
| 类型安全    | 运行时校验        | 编译期接口约束 |
| 漏实现方法  | 运行时才发现      | 编译报错       |
| 性能        | 反射 method.Call  | 直接调用       |
| msg_id 绑定 | 路由表 + 反射匹配 | 生成代码直接绑 |

### 实现入口

扩展 `tools/gen_routes`（或新建 `tools/gen_handler`）：

- 读 proto 的 `service` 定义（解析 FileDescriptor 的 service/method）+ message 的 msg_id option
- 生成 `XxxServiceHandler` 接口（方法签名来自 service rpc 定义）
- 生成 `RegisterXxxService(app, impl)` 函数（msg_id 来自 option，绑定方法）
- `returns (Empty)` 的 rpc 识别为 OneWay，接口方法签名去掉 resp 返回值

### 生成代码 / 业务实现分离（重要约定）

生成的是**接口**，业务**实现**接口，两者物理隔离，重新生成只覆盖生成文件，业务代码永不被覆盖。

**生成代码**（每次重新生成会覆盖，禁止手写）：

```
protocal/gen/world/
├── world.pb.go            # 消息结构（protoc 生成）
└── world_handler.gen.go   # ServiceHandler 接口 + RegisterXxxService 函数
```

- 粒度：一个 proto/service 对应一个 `.gen.go`，接口含该 service 所有方法

**业务实现**（手写，永不被覆盖）：

```
src/servers/worldsvr/internal/
├── world_handler.go    # 实现 WorldServiceHandler 接口
├── bag_handler.go      # 同一结构体的方法可拆到多个文件
└── combat_handler.go
```

- 粒度：业务自由拆分。推荐"一个实现结构体 + 按业务模块拆文件"——
  Go 允许同包同结构体的方法分散在多个文件，逻辑上一个 handler，物理上多文件

**proto 变更时的行为**：

- 加方法 → 接口多一个方法 → 业务 impl 编译报错（未实现接口）→ 强制补实现
- 删方法 → 接口少一个方法 → 业务旧实现变成多余方法（不报错，手动清理）

这正是 IDL 生成的核心价值：**proto 是唯一 source of truth，编译器强制实现与协议同步**。

---

## P1：生产环境必备

- **可观测性**：接入 Prometheus metrics（RPC 耗时/QPS/错误率）、OpenTelemetry tracing（已有 traceID 字段，未接入实际 tracing 系统）
- **集群层集成测试**：etcd + NATS 完整链路端到端测试，验证连接、断线重连、节点上下线
- **单元测试**：协议编解码、路由分发、session 管理、错误码映射等核心逻辑目前零测试覆盖
