# 配置工具链重设计

## 概述

本文档记录配置系统与构建工具链的整体重设计，涵盖：

1. `conf/values/<env>.yaml` 新增 `svr_list` 字段
2. 三脚本工具链：`config.py` / `build.py` / `update_bin.py`
3. proto schema 目录与文件重命名
4. 配置 proto 结构重构：Common 与服务私有配置彻底分离
5. `config_build` 渲染产物结构调整
6. `loader.go` 支持新的双文件加载模式
7. `config.pb` 改名为 `config.pb.descriptor`

---

## 一、svr_list — 本环境服务权威清单

### 位置

```yaml
# conf/values/dev.yaml
svr_list:
  - gatesvr
  - lobbysvr
  - onlinesvr
  - routersvr
```

### 约定

- `svr_list` 的每个值必须与以下三处**同名**：
  - `src/servers/<svr>/`（Go 编译入口）
  - `conf/<svr>.yaml`（服务配置模板）
  - `bin/<svr>`（编译产物，Windows 下含 `.exe`）
- `svr_list` **不进 `config_build` 渲染流程**，仅由 Python 脚本直接读取
- `svr_list` 按环境区分——dev/prod 可运行不同的服务子集

---

## 二、服务编排元数据 — SvrMeta

服务编排元数据（依赖前置、可执行路径等）**不单独存文件**，而是通过 proto `types.proto` 中的 `SvrMeta` message 定义，嵌入各服务自己的 proto 和 yaml 模板，随服务配置一起渲染到 `run/<svr>/conf/config.yaml`。

- 环境无关：`SvrMeta` 字段值写在 `conf/<svr>.yaml` 模板里（字面量），不从 `values/<env>.yaml` 取值
- `config.py` 和 launcher 在运行时直接读 `run/<svr>/conf/config.yaml` 的 `meta` 字段

---

## 三、proto schema 文件重命名

### 重命名对照

| 旧文件名                   | 新文件名            | 职责                           |
| -------------------------- | ------------------- | ------------------------------ |
| `config_options.proto`   | `options.proto`   | 自定义 option 扩展定义         |
| `config_common.proto`    | `common.proto`    | `CommonConfig` message       |
| `config_gatesvr.proto`   | `gatesvr.proto`   | `GateSvr` message            |
| `config_lobbysvr.proto`  | `lobbysvr.proto`  | `LobbySvr` message           |
| `config_onlinesvr.proto` | `onlinesvr.proto` | `OnlineSvr` message          |
| `config_routersvr.proto` | `routersvr.proto` | `RouterSvr` message          |
| —（新增）                 | `types.proto`     | 各服共用类型（`SvrMeta` 等） |

### gen_config 跳过规则更新

`gen_config` 遍历 FileDescriptorSet 时跳过：

- `google/` 前缀的文件
- `options.proto`（只含 option 扩展，无 message，改名前为 `config_options.proto`）

`types.proto` **不跳过**——其中的 `SvrMeta` 需要生成 Go struct，供各服务 proto 的嵌套字段引用（如 `GateSvr.Meta *SvrMeta`）。

### go_package 与包名变更

```proto
option go_package = "project/conf/schema/gen";
package conf;
```

生成的 Go 包名从 `config` 改为 `conf`，引用方式从 `config.GateSvr` 改为 `conf.GateSvr`。

影响 21 个文件，需全量替换。

---

## 四、proto 结构重构

### 新增 types.proto

```proto
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";

message SvrMeta {
  repeated string pre_application_list      = 1;
  string          pre_application_path      = 2;
  repeated string pre_mine_application_list = 3;
}
```

### 服务 proto 结构（以 gatesvr.proto 为例）

```proto
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "conf/schema/common.proto";
import "conf/schema/types.proto";
import "conf/schema/options.proto";

// GateSvr gatesvr 私有配置，不含 Common 字段。
message GateSvr {
  SvrMeta meta                = 1;
  string  node_id             = 2 [(conf.required) = true];
  string  server_type_name    = 3 [(conf.required) = true];
  string  addr                = 4 [(conf.required) = true];
  int32   heartbeat_sec       = 5 [(conf.reload) = true];
  int32   shutdown_timeout_sec = 6;
}
```

**关键原则：**

- 服务 proto 里**不再嵌套 `CommonConfig`**，彻底解耦
- message 命名规范：`GateSvr`、`LobbySvr`、`OnlineSvr`、`RouterSvr`（首字母大写 + Svr 后缀）
- 每个服务 proto 只有一个顶层 message，与 `src/servers/<svr>/`、`conf/<svr>.yaml` 同名对应

### common.proto 结构不变

```proto
message CommonConfig {
  Redis  redis  = 1;
  Etcd   etcd   = 2;
  Nats   nats   = 3;
  Mongo  mongo  = 4;
  Log    log    = 5;
  string version    = 6;
  string region     = 7;
  string excel_path = 8;
}
```

---

## 五、配置模板与渲染产物结构

### conf/ 模板（改后）

**`conf/common.yaml`**（只含公共字段）：

```yaml
redis:
  host: ${redis_host}
  port: ${redis_port}
  ...
etcd:
  endpoints: ${etcd_endpoints}
  ...
```

**`conf/gatesvr.yaml`**（只含私有字段，不含 common）：

```yaml
meta:
  pre_application_list: [redis, etcd, nats]
  pre_application_path: ./bin
  pre_mine_application_list: [routersvr]
node_id: ${gate_node_id}
server_type_name: gatesvr
addr: ${gate_addr}
heartbeat_sec: 30
shutdown_timeout_sec: 10
```

### run/ 渲染产物（改后）

```
run/
  common/conf/config.yaml     ← 只含 CommonConfig 字段
  gatesvr/conf/config.yaml    ← 只含 GateSvr 字段（含 meta）
  gatesvr/bin/
  gatesvr/log/
  lobbysvr/conf/config.yaml
  ...
```

**`run/common/conf/config.yaml`** 示例：

```yaml
redis:
  host: "127.0.0.1"
  port: 6379
etcd:
  endpoints: ["localhost:2379"]
...
```

**`run/gatesvr/conf/config.yaml`** 示例：

```yaml
meta:
  pre_application_list: [redis, etcd, nats]
  pre_application_path: ./bin
  pre_mine_application_list: [routersvr]
node_id: "1.1.1"
server_type_name: gatesvr
addr: "0.0.0.0:8888"
heartbeat_sec: 30
shutdown_timeout_sec: 10
```

### config_build 渲染逻辑变更

- **common**：单独渲染 `conf/common.yaml` → `run/common/conf/config.yaml`，不与任何服务合并
- **各服务**：只渲染 `conf/<svr>.yaml`（不再合并 common），输出到 `run/<svr>/conf/config.yaml`
- 深合并逻辑不再需要（每次只渲染单文件），占位符填充逻辑保持不变

---

## 六、loader.go 变更

### 新加载模式

框架层统一加载，服务只需声明自己的配置类型：

```go
// 框架启动时自动加载（所有服务共用）
commonCfg, err := config.LoadCommon("run/common/conf/config.yaml")

// 各服务只加载自己的私有配置
svrCfg, err := config.Load[conf.GateSvr]("run/gatesvr/conf/config.yaml")
```

- `LoadCommon` 固定路径、固定类型，框架层封装，服务无需感知
- `Load[T]` 泛型函数，类型安全，unmarshal 直接对应服务 proto 生成的 struct
- 两次加载完全独立，YAML 结构与 proto struct 一一对应，无需合并

---

## 七、三脚本工具链

### 执行顺序

```
python config.py --env=dev   # 首次：建目录 + 渲染配置
python build.py --env=dev    # 编译 + 铺二进制
```

### config.py --env=\<env\>

1. 读 `conf/values/<env>.yaml`，取 `svr_list`
2. **校验**：`svr_list` 中每个名字在 `src/servers/` 下有对应目录，否则报错退出
3. 渲染 `conf/common.yaml` → `run/common/conf/config.yaml`，建 `run/common/conf/` 目录
4. 对每个服务建 `run/<svr>/{bin,conf,log}/`（已存在则跳过）
5. 对每个服务调 `go run ./tools/config_build --env=<env> --svc=<svr>`
6. 调 `update_bin.py --env=<env>`（缺二进制软提示，正常退出）

### build.py --env=\<env\>

1. 读 `conf/values/<env>.yaml`，取 `svr_list`
2. **校验**：`svr_list` 中每个名字在 `src/servers/` 下有对应目录，否则报错退出
3. **校验**：`run/<svr>/` 目录存在，否则报错提示"请先执行 config.py"
4. 对每个服务 `go build -o bin/<svr>(.exe) ./src/servers/<svr>`，失败即报错退出
5. 调 `update_bin.py --env=<env>`

### update_bin.py --env=\<env\>

1. 读 `conf/values/<env>.yaml`，取 `svr_list`
2. 对每个服务：`bin/<svr>(.exe)` → `run/<svr>/bin/<svr>(.exe)`
3. `run/<svr>/bin/` 不存在 → 报错退出（目录应由 `config.py` 建好）
4. `bin/<svr>` 不存在 → 打印 `⚠ run/<svr> 目前不存在，可执行 ./build.py 进行编译生成`，继续，exit 0

### 报错覆盖汇总

| 脚本          | 场景                                          | 行为                                                                                |
| ------------- | --------------------------------------------- | ----------------------------------------------------------------------------------- |
| config.py     | `conf/values/<env>.yaml` 不存在             | 报错退出                                                                            |
| config.py     | `svr_list` 字段缺失                         | 报错退出                                                                            |
| config.py     | `svr_list` 某项在 `src/servers/` 下无目录 | 报错退出                                                                            |
| config.py     | `conf/<svr>.yaml` 不存在                    | `config_build` 报错，透传退出                                                     |
| config.py     | `bin/<svr>` 不存在（update_bin 阶段）       | ⚠ 软提示，不退出                                                                   |
| build.py      | `run/<svr>/` 不存在                         | 报错 + 提示先执行 `config.py`                                                     |
| build.py      | `go build` 编译失败                         | 报错退出，不调 `update_bin`                                                       |
| update_bin.py | `run/<svr>/bin/` 不存在                     | 报错退出                                                                            |
| update_bin.py | `bin/<svr>` 不存在                          | ⚠ 软提示「run/`<svr>` 目前不存在，可执行 ./build.py 进行编译生成」，继续，exit 0 |

---

## 八、config.pb.descriptor 改名

`conf/schema/gen/config.pb` → `conf/schema/gen/config.pb.descriptor`

需同步更新：

- `tools/gen_config/main.go`：默认 flag 值
- `tools/gen_config/gen_config_test.go`：3 处测试路径
- `development.md`、`architecture.md`、`docs/design/2026-06-03-config-system-design.md`：文档命令示例

---

## 九、svr_list 命名约定

`svr_list` 的每个值是跨五处的唯一标识符，**必须严格同名**：

| 用途         | 路径模式                    |
| ------------ | --------------------------- |
| Go 编译入口  | `src/servers/<svr>/`      |
| 配置模板     | `conf/<svr>.yaml`         |
| proto schema | `conf/schema/<svr>.proto` |
| 运行目录     | `run/<svr>/`              |
| 二进制       | `bin/<svr>(.exe)`         |

`config.py` 和 `build.py` 启动时均校验 `svr_list` 与 `src/servers/` 的一致性。

---

## 十、新增文件清单

```
conf/values/dev.yaml          ← 新增 svr_list 字段
conf/values/prod.yaml         ← 新增 svr_list 字段
conf/schema/types.proto       ← 新增，含 SvrMeta
conf/schema/options.proto     ← 改名自 config_options.proto
conf/schema/common.proto      ← 改名自 config_common.proto
conf/schema/gatesvr.proto     ← 改名自 config_gatesvr.proto（结构重构）
conf/schema/lobbysvr.proto    ← 改名自 config_lobbysvr.proto（结构重构）
conf/schema/onlinesvr.proto   ← 改名自 config_onlinesvr.proto（结构重构）
conf/schema/routersvr.proto   ← 改名自 config_routersvr.proto（结构重构）
config.py                     ← 新建
build.py                      ← 新建
update_bin.py                 ← 新建
```
