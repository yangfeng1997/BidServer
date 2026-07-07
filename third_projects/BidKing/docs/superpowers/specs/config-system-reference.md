# 配置系统参考文档

> 版本：v2.0（已过期）
> 日期：2026-06-19
> **本文档已被 [2026-06-21-config-system-refactor.md](2026-06-21-config-system-refactor.md) 取代。**
> 此处描述的是重构前的旧配置系统（map[string]any 存储、DiffFields/ValidateConfig 反射校验、
> Common[T]/Startup[T] 泛型访问），仅保留作历史参考。

---

## 一、设计原则

1. **单一真相源**：proto 是配置结构的唯一定义，Go struct 和元数据表全部由 gen_config 生成，不手写
2. **单一热更机制**：热更能力由 proto field option `reload=true` 决定，与文件名、目录结构无关
3. **构建期烘焙**：`${lower_case}` 占位符在构建期全部替换，运行时 yaml 只剩 `${UPPER_CASE}` 等待 OS 环境变量注入
4. **失败即报错**：values 找不到 key 报错退出；热更时静态字段变化整次拒绝
5. **目录即配置组**：`--config` 接受目录路径，自动 glob 所有 `*.yaml` 并 sort 保证顺序

---

## 二、四层架构

```
Proto Schema（单一真相源）
      ↓ protoc --descriptor_set_out --include_imports
FileDescriptorSet（conf/schema/gen/config.pb.descriptor，gitignore）
      ↓ gen_config
conf/schema/gen/config.go + reload_table.go
      ↓ config_build（Go）+ config.py（Python 编排）
run/{svr}/conf/*.yaml（烘焙产物，gitignore）
      ↓ config.Module → configgen.LoadFiles（运行时）
      ├─ Common()    — 公共配置，不热更
      └─ Startup()   — 服务配置，SIGHUP 热更
```

---

## 三、目录结构

### conf/（进 Git）

```
conf/
├── schema/
│   ├── options.proto            # field options: reload / required / env / enum_values
│   ├── common.proto             # CommonConfig + NodeConfig / EtcdConfig / RedisConfig
│   ├── types.proto              # 共享类型: LogConfig / LogGroupConfig
│   ├── gatesvr.proto            # GatesvrConfig + GateConfig
│   ├── lobbysvr.proto           # LobbysvrConfig + LobbyConfig
│   ├── roomsvr.proto            # RoomsvrConfig + RoomConfig
│   ├── matchsvr.proto           # MatchsvrConfig + MatchConfig
│   ├── onlinesvr.proto          # OnlinesvrConfig + OnlineConfig
│   ├── routeragent.proto        # RouteragentConfig + RouterAgentConfig
│   └── gen/                     # gen_config 输出，勿手动修改
│       ├── config.go            # 所有 Go struct
│       └── reload_table.go      # 5 张静态表 + FieldToMessage 映射
│
├── values/
│   ├── dev.yaml                 # svr_list + 所有 ${lower_case} 占位符值
│   ├── test.yaml
│   └── prod.yaml
│
├── secrets/
│   ├── .gitkeep
│   └── dev.env.example          # 列出所有 ${UPPER_VAR}，值留空
│
├── common/
│   └── common.yaml              # 公共配置模板（node/etcd/redis），独立烘焙
│
└── servers/
    ├── gatesvr/
    │   ├── gatesvr.yaml         # GatesvrConfig.gate
    │   └── gatesvr_log.yaml     # GatesvrConfig.log
    ├── lobbysvr/
    │   ├── lobbysvr.yaml
    │   └── lobbysvr_log.yaml
    └── ...
```

目录说明：
- `schema/` 只放 proto 源文件和 gen/ 产物
- `types.proto` 存放各服务 proto 共用的类型（LogConfig / LogGroupConfig）
- `common.proto` 定义 CommonConfig——对应 `common.yaml`，独立加载
- `servers/{svr}/` 下按子 message 自由拆文件，加文件即自动加载

### run/（gitignore，部署产物）

```
run/
├── common/conf/
│   └── common.yaml              # 烘焙后
├── gatesvr/
│   ├── bin/
│   ├── conf/
│   │   ├── gatesvr.yaml         # ${lower_case} 已填，${UPPER} 保留
│   │   └── gatesvr_log.yaml
│   └── log/                     # 运行时日志输出
└── ...
```

---

## 四、Proto Schema 层

### 4.1 options.proto

```protobuf
syntax = "proto3";
package conf.schema;
import "google/protobuf/descriptor.proto";
option go_package = "project/conf/schema/gen;gen";

extend google.protobuf.FieldOptions {
  bool reload      = 50005;  // 字段可热更
  bool required    = 50006;  // 必填，零值启动时报错
  bool env         = 50007;  // 运行时从 ${UPPER_VAR} 注入，必须是 string 类型
  string enum_values = 50008; // 允许值列表，逗号分隔
}
```

### 4.2 common.proto — 公共配置消息

```protobuf
syntax = "proto3";
package conf.schema;
import "conf/schema/options.proto";
option go_package = "project/conf/schema/gen;gen";

message CommonConfig {
  NodeConfig  node  = 1;
  EtcdConfig  etcd  = 2;
  RedisConfig redis = 3;
}

message NodeConfig {
  uint32 world_id     = 1 [(conf.schema.required) = true];
  uint32 server_type  = 2 [(conf.schema.required) = true];
  uint32 server_index = 3;
}

message EtcdConfig {
  repeated string endpoints = 1 [(conf.schema.required) = true];
}

message RedisConfig {
  string host     = 1 [(conf.schema.required) = true];
  int32  port     = 2 [(conf.schema.required) = true];
  string password = 3 [(conf.schema.required) = true, (conf.schema.env) = true];
}
```

### 4.3 types.proto — 共享类型

```protobuf
syntax = "proto3";
package conf.schema;
import "conf/schema/options.proto";
option go_package = "project/conf/schema/gen;gen";

message LogConfig {
  string level         = 1 [(conf.schema.enum_values) = "debug,info,warn,error,fatal"];
  string format        = 2 [(conf.schema.enum_values) = "console,json"];
  bool   stderr_also   = 3;
  string dir           = 4 [(conf.schema.required) = true];
  string basename      = 5 [(conf.schema.required) = true];
  int32  max_size_mb   = 6;
  int32  max_backups   = 7;
  bool   rotate_by_hour = 8;
}

message LogGroupConfig {
  LogConfig main    = 1;
  LogConfig res     = 2;
  LogConfig tracing = 3;
}
```

### 4.4 服务 proto（以 gatesvr.proto 为例）

```protobuf
syntax = "proto3";
package conf.schema;
import "conf/schema/types.proto";
import "conf/schema/options.proto";
option go_package = "project/conf/schema/gen;gen";

message GatesvrConfig {
  GateConfig     gate = 1;
  LogGroupConfig log  = 2;
}

message GateConfig {
  string listen_tcp        = 1 [(conf.schema.required) = true];
  string listen_ws         = 2 [(conf.schema.required) = true];
  int32  drain_timeout_sec = 3;
  int32  max_conn          = 4 [(conf.schema.required) = true, (conf.schema.reload) = true];
  string log_level         = 5 [(conf.schema.reload) = true, (conf.schema.enum_values) = "debug,info,warn,error"];
  int32  heartbeat_sec     = 6 [(conf.schema.reload) = true];
}
```

其余服务结构相同：`LobbysvrConfig` / `RoomsvrConfig` / `MatchsvrConfig` / `OnlinesvrConfig` / `RouteragentConfig`，各自包含服务专有 Config + `LogGroupConfig log`。

字段级规则：
- 任何 proto 文件的任何字段都可以标 `reload=true`，热更能力由 option 决定，与文件名无关
- `env=true` 的字段必须是 string 类型，否则 gen_config 编译报错
- 有 `required=true` 的字段，零值启动时校验报错

---

## 五、Gen 层（gen_config）

### 5.1 生成 FileDescriptorSet

```bash
protoc \
  --proto_path=. \
  --descriptor_set_out=conf/schema/gen/config.pb.descriptor \
  --include_imports \
  conf/schema/*.proto
```

gen_config 使用 `google.golang.org/protobuf/proto` + `descriptorpb` 解析，支持跨文件 import，所有 message 按名字去重后统一生成到 `gen` 包。

### 5.2 生成 config.go

```go
// Code generated by gen_config. DO NOT EDIT.
package gen

type CommonConfig struct {
    Node  *NodeConfig  `yaml:"node"`
    Etcd  *EtcdConfig  `yaml:"etcd"`
    Redis *RedisConfig `yaml:"redis"`
}

type LogGroupConfig struct {
    Main    *LogConfig `yaml:"main"`
    Res     *LogConfig `yaml:"res"`
    Tracing *LogConfig `yaml:"tracing"`
}

type GatesvrConfig struct {
    Gate *GateConfig     `yaml:"gate"`
    Log  *LogGroupConfig `yaml:"log"`
}

type GateConfig struct {
    ListenTcp       string `yaml:"listen_tcp"`
    ListenWs        string `yaml:"listen_ws"`
    DrainTimeoutSec int32  `yaml:"drain_timeout_sec"`
    MaxConn         int32  `yaml:"max_conn"`
    LogLevel        string `yaml:"log_level"`
    HeartbeatSec    int32  `yaml:"heartbeat_sec"`
}
```

### 5.3 生成 reload_table.go — 五张静态表 + FieldToMessage

```go
// ReloadableFields — message 名 → 字段名 → 可热更
var ReloadableFields = map[string]map[string]bool{
    "GateConfig":  {"max_conn": true, "log_level": true, "heartbeat_sec": true},
    "LobbyConfig": {"log_level": true, "heartbeat_sec": true, "max_player": true},
    // ...
}

// RequiredFields — message 名 → 字段名 → 必填
var RequiredFields = map[string]map[string]bool{
    "NodeConfig": {"world_id": true, "server_type": true},
    "EtcdConfig": {"endpoints": true},
    "RedisConfig": {"host": true, "port": true, "password": true},
    "GateConfig": {"listen_tcp": true, "listen_ws": true, "max_conn": true},
}

// EnvFields — message 名 → 字段名 → 运行时环境变量注入
var EnvFields = map[string]map[string]bool{
    "RedisConfig": {"password": true},
}

// EnumFields — message 名 → 字段名 → 允许值列表
var EnumFields = map[string]map[string][]string{
    "GateConfig": {"log_level": {"debug", "info", "warn", "error"}},
    "LogConfig":  {"level": {"debug", "info", "warn", "error", "fatal"}},
    // ...
}

// FieldToMessage — yaml 字段名 → message 类型名（供 DiffFields 精确定位）
var FieldToMessage = map[string]string{
    "etcd":  "EtcdConfig",
    "gate":  "GateConfig",
    "log":   "LogGroupConfig",
    "node":  "NodeConfig",
    "redis": "RedisConfig",
    // ...
}
```

---

## 六、Build 层（占位符与渲染规则）

### 6.1 Values 文件

`conf/values/{env}.yaml` — 所有服务共用，统一定义所有占位符值 + svr_list：

```yaml
svr_list:
  - gatesvr
  - lobbysvr
  - roomsvr
  - matchsvr
  - onlinesvr
  - routeragent

redis_host: "127.0.0.1"
redis_port: 6379
etcd_endpoint: "127.0.0.1:2379"
gate_listen_tcp: "0.0.0.0:7001"
gate_listen_ws:  "0.0.0.0:7002"
log_dir: "./logs"
```

`svr_list` 不进 config_build 渲染流程，仅由 Python 脚本直接读取。

### 6.2 占位符三类

| 格式 | 处理时机 | 处理方式 | 找不到时 |
|---|---|---|---|
| `${lower_case}` | 构建期 | 从 values 文件替换 | **报错退出** |
| `${UPPER_CASE}` | 运行时 | OS 环境变量替换 | 启动时报错 |
| `${Mixed_Case}` | 构建期 | 禁止 | 报错退出 |

### 6.3 烘焙流程（config_build）

```
① 加载 conf/values/{env}.yaml → flatten 为点路径 key
② config_build --common → 烘焙 common/common.yaml → run/common/conf/common.yaml
③ config_build --svr {svr} → glob servers/{svr}/*.yaml，逐文件烘焙：
   - 单次扫描检测 ${Mixed_Case} → 报错退出
   - 替换 ${lower_case} → 找不到 key 报错退出
   - ${UPPER_CASE} 保留原样
④ 输出到 run/{svr}/conf/
```

### 6.4 YAML 模板示例

`conf/common/common.yaml`：
```yaml
node:
  world_id: 1
  server_type: 1
  server_index: 0

etcd:
  endpoints:
    - "${etcd_endpoint}"

redis:
  host: "${redis_host}"
  port: ${redis_port}
  password: "${REDIS_PWD}"
```

`conf/servers/gatesvr/gatesvr.yaml`：
```yaml
gate:
  listen_tcp: "${gate_listen_tcp}"
  listen_ws:  "${gate_listen_ws}"
  drain_timeout_sec: 5
  max_conn: 10000
  log_level: info
  heartbeat_sec: 30
```

`conf/servers/gatesvr/gatesvr_log.yaml`：
```yaml
log:
  main:
    level: info
    format: console
    stderr_also: true
    dir: "${log_dir}"
    basename: gatesvr
    max_size_mb: 100
    max_backups: 72
    rotate_by_hour: true
  res:
    level: info
    format: console
    dir: "${log_dir}"
    basename: gatesvr_res
    max_size_mb: 100
    max_backups: 72
    rotate_by_hour: true
  tracing:
    level: debug
    format: json
    dir: "${log_dir}"
    basename: gatesvr_trace
    max_size_mb: 200
    max_backups: 168
    rotate_by_hour: true
```

---

## 七、Runtime 层

### 7.1 初始化

```go
// cmd/gatesvr/main.go
f.StringArrayVarP(&configFiles, "config", "c",
    []string{"run/common/conf/", "run/gatesvr/conf/"},
    "config directories (all *.yaml loaded)")
```

`config.NewModule` 将路径展开为文件列表——目录 glob `*.yaml` 并 sort——然后按路径自动分流：

```
包含 "/common/" → commonFiles → Common()  (不热更，只读)
其余            → startupFiles → Startup() (SIGHUP 热更)
```

### 7.2 类型化访问

内部存储为 `map[string]any`（通用），对外提供类型化转换：

```go
// 公共配置（不热更）
common := config.Common[*gen.CommonConfig]()
common.Node.WorldId       // uint32

// 服务配置（SIGHUP 可能更新）
cfg := config.Startup[*gen.GatesvrConfig]()
cfg.Gate.MaxConn          // int32
cfg.Log.Main.Basename     // string

// 也可用 map 访问（零拷贝，框架基础设施用）
raw := config.Startup[map[string]any]()
```

### 7.3 热更保护流程

```
SIGHUP 触发
  ↓
重新 LoadFiles(startupFiles...) → newCfg
  ↓
DiffFields(oldCfg, newCfg, ReloadableFields, FieldToMessage)
  ↓
  有非 reloadable 字段变化 → 整次拒绝，保留 oldCfg，打错误日志
  仅 reloadable 字段变化    → diff + DiffFields 递归全深度校验
  ↓
ValidateConfig(newCfg, RequiredFields, EnumFields, FieldToMessage)
  ↓
globalStartup.Store(newCfg)
```

DiffFields 支持 struct 和 `map[string]any` 双形态，通过 `FieldToMessage` 精确定位当前层级的 message 类型。`commonFiles` 不参与热更——只有 `startupFiles` 被 SIGHUP 重新加载。

### 7.4 目录加载

路径可以是文件或目录。目录自动 glob 所有 `*.yaml` 并 sort，多个文件通过 `deepMerge` 递归合并：

```go
// 目录输入
config.NewModule([]string{"run/common/conf/", "run/gatesvr/conf/"})

// resolveConfigPath:
//   "run/common/conf/" → glob *.yaml → sort → [common.yaml]
//   "run/gatesvr/conf/" → glob *.yaml → sort → [gatesvr.yaml, gatesvr_log.yaml]
```

`deepMerge` 对嵌套 map 递归合并，同名字段后来覆盖，不同名保留。往 `gatesvr/conf/` 加任何 `.yaml` 文件自动加载，无需改代码。

### 7.5 运行时环境变量注入

`configgen.ExpandUpperEnv` 在 yaml.Unmarshal 之前展开所有 `${UPPER_CASE}`，找不到则启动报错。

本地开发：`source secrets/dev.env` 后启动。K8s 生产：Secrets 挂载为环境变量。

---

## 八、Secrets 管理

```
conf/secrets/
├── .gitkeep
└── dev.env.example    # 进仓库，列出所有 ${UPPER_VAR}，值留空
```

`.gitignore`：
```
conf/secrets/*.env
!conf/secrets/*.env.example
```

`dev.env.example`：
```bash
REDIS_PWD=
```

---

## 九、CLI Flags（cobra）

| Flag | 短名 | 类型 | 默认值 | 说明 |
|------|------|------|--------|------|
| `--config` | `-c` | stringArray | `["run/common/conf/", "run/{svr}/conf/"]` | 配置目录或文件（目录 auto-glob `*.yaml`） |
| `--server-index` | — | int32 | `-1`（不覆盖） | 覆盖 `node.server_index` |
| `--validate-config` | — | bool | false | 校验配置文件后退出（CI 用） |

---

## 十、日志系统

### 10.1 三个独立实例

每个服务创建三个 zap logger 实例，写入独立文件：

```yaml
# 由 proto 定义: LogGroupConfig { main, res, tracing }
log:
  main:     → gatesvr.log       (console, info)
  res:      → gatesvr_res.log   (console, info)
  tracing:  → gatesvr_trace.log (JSON, debug)
```

`log.Module.OnAfterInit` 从 `config.Startup()` 读 `log` 段，调用 `logger.NewZapFileLogger` 创建三个实例，`Main` 同时设为全局默认。

### 10.2 运行时调整日志级别

zap 使用 `AtomicLevel`，热更时调 `SetLevel()`——不换实例、不关文件、无锁：

```go
// pkg/logger
func (b *ZapBackend) SetLevel(l Level) { b.atomLevel.SetLevel(toZapLevel(l)) }
func (c *LogCloser) SetLevel(l Level)  { c.zb.SetLevel(l) }
```

### 10.3 包级访问

```go
// 三个命名实例，OnAfterInit 之后只读
log.Main.Info("msg", logger.Int("room", 1))
log.Res.Info("resource acquired")
log.Tracing.Info("trace span")

// 包级快捷函数打到全局 Logger（= Main）
logger.Info("server started")     // = log.Main.Info(...)
logger.Infof("port: %d", 8080)    // = log.Main.Infof(...)
```

---

## 十一、编排层

### 入口

```bash
# ① 烘焙配置（仅此命令需指定环境，默认 dev）
make config ENV=dev

# ② 编译 + 铺二进制（自动读取 run/ENV 中的环境）
make build

# 清空部署产物
make run-clean

# 或直接调用脚本
python3 scripts/config.py --env=dev
python3 scripts/build.py          # 无参数，从 run/ENV 读取
```

`config.py` 执行后写入 `run/ENV` 并设为只读（`chmod 444`）。

### svr_list

`conf/values/{env}.yaml` 顶层 `svr_list` 字段，由 Python 脚本读取。

### config.py --env=\<env\>

默认值：`dev`

1. 读 `conf/values/{env}.yaml`，取 `svr_list`
2. 校验 `svr_list` 每项在 `cmd/` 下有对应目录
3. 调 `config_build --common --env={env}` 烘焙 common.yaml
4. 对每个 svr 建 `run/{svr}/{bin,conf,log}/`
5. 对每个 svr 调 `config_build --svr={svr} --env={env}`
6. 写 `run/ENV` 并设为只读

### build.py

无参数。从 `run/ENV` 读取环境名。`run/ENV` 不存在则报错退出。

1. 读 `svr_list`，校验 `run/{svr}/` 目录存在
2. 对每个 svr `go build -o build/{svr} ./cmd/{svr}`
3. `build/{svr}` → `run/{svr}/bin/{svr}`，缺失二进制打印警告继续

---

## 十二、代码文件速查表

| 文件 | 职责 |
|------|------|
| `conf/schema/options.proto` | field option 扩展定义 |
| `conf/schema/common.proto` | CommonConfig + Node/Etcd/Redis |
| `conf/schema/types.proto` | LogConfig + LogGroupConfig |
| `conf/schema/{svr}.proto` ×6 | 各服务配置消息 |
| `conf/schema/gen/config.go` | gen_config 生成：所有 Go struct |
| `conf/schema/gen/reload_table.go` | gen_config 生成：5 张静态表 + FieldToMessage |
| `conf/values/{env}.yaml` | 占位符值池 |
| `conf/common/common.yaml` | 公共配置模板 |
| `conf/servers/{svr}/*.yaml` | 服务配置模板（自由拆文件） |
| `conf/secrets/dev.env.example` | 运行时环境变量清单 |
| `tools/gen_config/main.go` | FileDescriptorSet → Go struct + 元数据表 |
| `tools/config_build/main.go` | 构建期占位符烘焙 |
| `pkg/configgen/configgen.go` | Load / LoadFiles / deepMerge / DiffFields / ValidateConfig |
| `internal/core/config/module.go` | 目录加载 → 分流 → 快照 → SIGHUP 热更 |
| `internal/core/log/module.go` | 从配置创建三个 Logger 实例 |
| `pkg/logger/` | zap 封装：Logger / Backend / Rotate / SugaredLogger |
| `scripts/config.py` / `scripts/build.py` | Python 编排脚本 |
| `Makefile` | 快捷入口：`make config` / `make build` / `make run-clean` |
| `tools/gen_proto.sh` | 完整 proto 生成流程 |
