# 配置系统重构设计文档

> 版本：v2.0（已实现）
> 日期：2026-06-22
> 关联：[config-system-reference.md](config-system-reference.md)（当前实现 v2.0）、[framework-design.md](framework-design.md)

**状态**：本 spec 描述的目标架构已全部实现。配置系统退化为纯粹的 load + reload——只负责将 YAML 加载进 typed struct 并在 SIGHUP 时重新加载。yaml 里写什么值、如何维护不同副本的配置，是部署系统的事。

**范围**：本文档只覆盖**服务器配置**（监听地址、超时、连接数、日志级别、心跳间隔等服务运行配置）。**策划配置表**（Excel + protobuf 导出）是业务相关、独立项目，不在本文档范围，后续单独设计，不与服务器配置混在一起。

---

## 一、动机

当前配置系统（参考 [config-system-reference.md](config-system-reference.md)）以 proto 为单一真相源，方向正确，但运行时实现有三个结构性问题：

### 问题 1：`map[string]any` 中间层 + 双重序列化

`pkg/configgen/configgen.go` 的 `LoadFiles[T]` 先把 YAML 解析成 `map[string]any`，再 `yaml.Marshal` + `Unmarshal` 回具体 struct。`convertFromMap` 每次调用都做一次 marshal/unmarshal，热路径上完全不可接受。

### 问题 2：泛型 `Common[T]()` / `Startup[T]()` 热路径

```go
config.Common[gen.CommonConfig]()    // 每次调用走 reflect + 类型断言
config.Startup[gen.GateConfig]()
```

读配置是最高频的操作之一，不应有反射开销。

### 问题 3：公共配置与服务配置混在一个 Module

`config.Module` 同时持有 `globalCommon`（不可热更）和 `globalStartup`（SIGHUP 热更），但二者生命周期、校验时机、可见性完全不同，混在一起导致 reload 逻辑要兼顾两套语义。

### 问题 4：热更校验依赖运行时 map 递归 diff

`DiffFields` 在 `map[string]any` 上递归，依赖 `gen.FieldToMessage` 反查结构类型。该映射漏配会静默误判，且整次遍历成本高。

---

## 二、设计目标

1. **零反射读路径**：`config.GateService()` 返回具体 `*GateSvcConfig` 指针，仅一次 `atomic.Pointer.Load`
2. **零泛型**：消灭 `Common[T]` / `Startup[T]`，全部由 codegen 生成具体函数
3. **零 `map[string]any`**：YAML 直达具体 struct，无中间层、无 double-marshal
4. **分层加载**：CommConfig（不可热更）与 ServiceConfig（可热更）物理分离
5. **双 buffer 热更**：load → check → swap 三段式，任一段失败不污染当前副本
6. **编译期 reload 约束**：reload 标记从运行时 map 查找变成 codegen 固化的字段比较
7. **读路径用 `atomic.Pointer[T]`**：裸指针无 interface 装箱，优于 `atomic.Value`

---

## 三、关于 reload 标记（重点回应）

### 当前行为

`reload` 是 proto field option（`conf/schema/options.proto:10`，编号 50005）。codegen 把它导出成 `gen.ReloadableFields` 查找表。热更时 `DiffFields` 在 `map[string]any` 上递归，**任何未标记 reload 的字段发生变化 → 整次 reload 拒绝**，报 `static fields changed (reload rejected): [gate.listen_tcp]`（`internal/core/config/module.go:210`）。

例：`GateConfig.listen_tcp` 未标记 reload，`max_conn` 标记了 reload。热更时若 yaml 里 `listen_tcp` 变了，即便 `max_conn` 也变了，整次拒绝，旧配置保持不变。

### 新方案：语义完整保留，实现升级为编译期

reload 标记继续在 proto 里写 `[(conf.schema.reload) = true]`，但 codegen 不再生成 `ReloadableFields` 查找表，而是**直接生成静态字段比较代码**，嵌入 Loader 的 `Check()` 阶段：

```go
// codegen 生成
func (l *gateSvcLoader) Check() error {
    cur := l.current.Load() // *GateSvcConfig，atomic.Pointer 无类型断言

    // 静态字段（未标 reload）：变了就拒绝整次热更
    if l.shadow.ListenTcp != cur.ListenTcp {
        return fmt.Errorf("static field gate.listen_tcp changed (reload rejected)")
    }
    if l.shadow.ListenWs != cur.ListenWs {
        return fmt.Errorf("static field gate.listen_ws changed (reload rejected)")
    }
    if l.shadow.DrainTimeoutSec != cur.DrainTimeoutSec {
        return fmt.Errorf("static field gate.drain_timeout_sec changed (reload rejected)")
    }

    // reload 字段（max_conn / log_level / heartbeat_sec）：不检查差异，这是热更的目的
    return l.shadow.Validate()
}
```

### 对比

| 维度 | 当前（map diff） | 新方案（codegen 编译期） |
|------|------------------|---------------------------|
| reload 标记载体 | 运行时 `ReloadableFields` map | 编译期生成的 if 分支 |
| 检测方式 | `map[string]any` 递归 + `FieldToMessage` 反查 | 直接字段比较，类型安全 |
| 性能 | O(全字段) 遍历 + 路径字符串拼接 | O(静态字段数) 整数比较 |
| 错误信息 | `[gate.listen_tcp]` 路径列表 | 精确到单个字段的 error |
| 漏配风险 | `FieldToMessage` 漏配静默误判 | proto 即真相，不可能漏 |
| 语义 | 改静态字段 → 整次拒绝 | 改静态字段 → 整次拒绝（一致） |

**结论：用户提到的"标记 reload 但热更报错"行为在新方案中完整保留并强化。** 静态字段被改动时仍会拒绝整次热更，保护运行中服务不因改了监听端口等不可热更字段而崩溃。

### 可选增强（非默认）

当前"改任一静态字段 → 整次拒绝"是安全默认。若后续需要更宽松策略，可在 Check 返回的 error 里附带 reloadable 字段是否也变了的信息，由上层决定是"拒绝并提示重启"还是"应用 reload 部分、忽略 static 部分"。**默认仍为整次拒绝**，与现网行为一致。

---

## 四、目标架构

### 分层：CommConfig vs ServiceConfig

参考 C++ 项目 `CommConfMgr`（不注册 TableLoaderManager，自己 Load，不可热更）与 `ScenesvrRuntimeConfMgr`（`REGISTER_TABLE`，参与双 buffer 热更）的分层：

```
CommConfig（不可热更）
  ├─ 启动一次加载
  ├─ atomic.Pointer[CommConfig] 存指针，启动后永不替换
  └─ 独立包，不进 ConfigManager

ServiceConfig（可热更）
  ├─ 注册到 ConfigManager
  ├─ 参与热更 load → check → swap
  └─ 每服务一个 Loader，由 codegen 生成
```

### 配置链路

服务器配置统一走 YAML，单一格式、单一解析器、单一 codegen 路径：

```
proto schema（单一真相源，含 reload/required/env options）
      ↓ protoc
FileDescriptorSet
      ↓ gen_config（已有，扩展输出 loader 代码）
conf/schema/gen/*.gen.go（具体 struct + Loader 实现）
      ↓
运行时：yaml.Unmarshal → 具体 struct（无 map[string]any 中间层）
      ↓ atomic.Pointer[T] 存指针
读路径：atomic.Pointer.Load，零分配零反射零装箱
```

**存储格式**：服务运行配置统一用 YAML——人类可读、ops 可手编、无需导表工具链。codegen 生成具体 struct 直接 unmarshal，不走 `map[string]any`，也无 double-marshal。

> 策划配置表（Excel + protobuf）是独立项目，不在此链路内，后续单独设计。

---

## 五、ConfigManager 双 buffer 热更

```go
// internal/core/config/manager.go
type ConfigManager struct {
    mu      sync.Mutex
    loaders []Loader
}

type Loader interface {
    Load() error   // 加载到自身影子副本（先写 local 再提交，失败不污染）
    Check() error  // 全量校验 + reload 约束检查（对比影子与 current）
    Swap()         // 原子替换 current
}

func (m *ConfigManager) Reload() error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 1. 全部 Load — 任一失败立即返回，影子副本状态不保证一致，但不影响 _current
    for _, l := range m.loaders {
        if err := l.Load(); err != nil {
            return fmt.Errorf("reload load: %w", err)
        }
    }
    // 2. 全部 Check — reload 约束 + 业务校验，任一失败不 Swap
    for _, l := range m.loaders {
        if err := l.Check(); err != nil {
            return fmt.Errorf("reload check: %w", err)
        }
    }
    // 3. 全部 Swap — 读路径从此刻起拿到新值
    for _, l := range m.loaders {
        l.Swap()
    }
    return nil
}
```

读路径与写路径完全分离：
- **读路径**：`atomic.Pointer.Load` 拿指针，无锁、无分配、无 interface 装箱
- **写路径**（SIGHUP）：`sync.Mutex` 仅保护并发 reload 之间互斥，不阻塞读

---

## 六、codegen 产出（消灭泛型）

`protoc-gen-cfgloader`（或扩展现有 `gen_config`）为每个 proto message 生成：

```go
// internal/gatesvr/config/config.gen.go — codegen 产出
// shadow 与 current 封装在 Loader struct 内，状态与行为内聚，无包级可变全局
type gateSvcLoader struct {
    shadow  GateSvcConfig                 // 影子副本，Load 写、Check 读
    current atomic.Pointer[GateSvcConfig] // 当前副本，Swap 后替换，读路径 Load
}

// 读路径 — 零反射零泛型零装箱
// gateSvc 单例，codegen 同时生成包级实例与访问函数
var gateSvc = &gateSvcLoader{}

func GateService() *GateSvcConfig {
    return gateSvc.current.Load()
}

func (l *gateSvcLoader) Load() error {
    data, err := os.ReadFile("run/gatesvr/conf/svc.yaml")
    if err != nil { return err }
    fresh := GateSvcConfig{}
    if err := yaml.Unmarshal(data, &fresh); err != nil { return err } // 直达具体 struct，无 map 中间层
    l.shadow = fresh // 先写 local 再提交，失败不污染影子
    return nil
}

func (l *gateSvcLoader) Check() error {
    cur := l.current.Load() // *GateSvcConfig，atomic.Pointer 无类型断言
    // 静态字段 reload 约束（见第三节）
    if l.shadow.ListenTcp != cur.ListenTcp {
        return fmt.Errorf("static field gate.listen_tcp changed (reload rejected)")
    }
    // ... 其他静态字段
    return l.shadow.Validate() // required / enum / 业务校验
}

func (l *gateSvcLoader) Swap() { l.current.Store(&l.shadow) }
```

CommConfig 同理但不进 ConfigManager（按生命周期分离，不为统一而统一）：

```go
// internal/core/config/comm.gen.go — codegen 产出
type commLoader struct {
    current atomic.Pointer[CommConfig]
}
var comm = &commLoader{}

func Comm() *CommConfig { return comm.current.Load() }

// 启动一次加载，不注册 Loader，不参与 Reload
func LoadComm(files []string) error { ... }
```

---

## 七、迁移路径（可分阶段，不推倒重来）

| 阶段 | 工作量 | 价值 | 风险 |
|------|--------|------|------|
| **1. 删 `convertFromMap` double-marshal** | 小 | 立即解决性能问题 | 低，行为不变 |
| **2. CommConfig 独立包 + `atomic.Pointer[T]` 具体指针** | 中 | 读路径零锁零装箱，物理分层 | 中，需迁移调用点 |
| **3. ConfigManager + Loader 接口 + 三段式热更** | 中 | 双 buffer 安全热更 | 中，reload 语义迁移 |
| **4. codegen 生成 Loader，消灭泛型与 map diff** | 大 | 类型安全，reload 编译期固化 | 高，codegen 改动大 |

阶段 1 可立即独立交付。阶段 2-3 是下一个 sprint 的核心。阶段 4 是最终形态，与代码生成器迭代推进。

> 策划配置表（Excel + protobuf）是独立项目，不列入本迁移路径，后续单独规划。

---

## 八、跨语言兼容性（Go / C++ 共存，服务器配置范围）

本方案设计上向 C++ 项目 `TableLoaderManager` / `CommConfMgr` / `ScenesvrRuntimeConfMgr` 靠拢，重构后 Go 侧与 C++ 侧构成镜像关系。**服务器配置范围内**，跨语言共享的是 proto schema 与语义规则，落盘格式统一为 YAML。

### 8.1 天然兼容（语义层 + schema 层）

| 层 | 共享内容 | 兼容原因 |
|----|----------|----------|
| proto schema | 同一份 `conf/schema/*.proto` | proto 语言无关，Go 和 C++ 都从同一份 `.proto` 生成 struct |
| field options | `reload` / `required` / `env` / `enum_values`（编号 50005-50008） | 自定义 option 编进 FileDescriptorSet，C++ `GetExtension()` 与 Go 反射读取结果一致 |
| reload 语义 | 改静态字段 → 整次拒绝 | 规则而非实现，两边 codegen 各自生成比较代码，规则同一来源 |
| 双 buffer 算法 | load → check → swap | 语言无关，C++ `m_vLoadingTable` / `m_vCurrentTable` 即同一模式 |

### 8.2 实现不同，语义一致

| 维度 | Go 侧 | C++ 侧 |
|------|-------|---------|
| 读路径无锁 | `atomic.Pointer[T].Load()` | `std::atomic<Table*>` 或 RCU 读 |
| 写路径互斥 | `sync.Mutex` | `std::mutex` |
| codegen 产物 | `*.gen.go`（if 字段比较） | `TableLoader<T>` 模板 |
| 影子副本 | `gateSvcLoader.shadow` | `m_vLoadingTable` |

这些是语言惯用法差异，不影响跨服兼容。两边从同一份 proto schema 生成、消费同一份 YAML 文件。

### 8.3 不共享（本就不该共享）

- codegen 工具本身：Go 用 `gen_config`，C++ 用自己的生成器。但输入相同（同一 FileDescriptorSet），输出语义相同。
- 生成出的源码：不同语言，本就不同。

### 8.4 落盘格式：YAML 的跨语言代价

服务器配置统一用 YAML 落盘。跨语言影响：

| 场景 | 影响 |
|------|------|
| Go 服自用 | YAML 人类可读，ops 可手编，无问题 |
| C++ 服读同一份 YAML | C++ 需引入 YAML 解析器（如 yaml-cpp），且 YAML 整数/浮点解析跨语言有微妙差异 |

**当前立场**：服务器配置统一 YAML，优先 ops 可读性。若将来 C++ 服要读同一份服务器配置且想避免 YAML 解析器依赖，再单独评估是否对**这部分**改 proto 二进制——这是后续决策，不在本 spec 内固化。策划表的 Excel+protobuf 是另一条独立链路，不受此影响。

### 8.5 跨语言约束（必须守住）

1. **field option 编号固定**：50005-50008 一旦分配不可改，否则 Go 与 C++ 读出的 option 对不上。proto 扩展编号即契约。
2. **codegen 规则对齐**：Go 生成 Check() 时"哪些算静态字段"必须与 C++ 一致。只要都从同一份 proto 的 `reload` option 推导，就不会分歧。禁止手写映射表。
3. **YAML 跨语言解析差异**：若 C++ 服读同一份 YAML，整数/浮点/枚举字符串的解析需与 Go 侧对齐（同一份 proto schema 生成的 struct 约束类型，可缓解）。

### 8.6 结论

服务器配置范围内完全兼容，前提是守住三条线：
1. proto schema + field options 是唯一真相源，Go 和 C++ 都从它生成 struct
2. reload 规则由 codegen 从 proto option 推导，不手写映射表
3. 落盘 YAML 的跨语言解析差异由 proto schema 生成的强类型 struct 兜底

本重构使 Go 侧向 C++ 侧成熟设计靠拢，未来 C++ 服可复用同一套 `.proto` schema 与 reload 规则，仅各自 codegen、各自无锁读。

---

## 九、与现有 spec 的关系

- 本文档是 **重构设计**，描述目标态
- [config-system-reference.md](config-system-reference.md) 描述 **当前实现** v2.0，重构期间持续有效，逐步被本文档取代
- proto schema（`conf/schema/*.proto`）与 field options（`reload`/`required`/`env`/`enum_values`）**保持不变**，是跨新旧方案的稳定接口
- codegen 产物从 `gen/config.go` + `gen/reload_table.go` 演进为 per-server 的 `*.gen.go`（含 Loader）

---

## 十、待决问题

1. **CommConfig 是否真不可热更**：当前 spec 假设公共配置启动后冻结。若 etcd/redis 连接串需热更，是否单独走 ConfigManager？倾向：连接串归 ServiceConfig，CommConfig 只放真正静态的全局项。
2. **C++ 服读服务器配置的格式**：当前统一 YAML。若 C++ 服要复用且不想引入 YAML 解析器，是否对这部分改 proto 二进制——后续按需评估，不在本 spec 固化。
3. **Loader 注册时机**：codegen 生成 `init()` 自注册 vs 显式 `Register`。倾向显式注册，便于测试与依赖可见。

下一步：基于本文档启动 brainstorming 细化阶段 1-3 的实现 spec，或直接进入 writing-plans 产出实现计划。
