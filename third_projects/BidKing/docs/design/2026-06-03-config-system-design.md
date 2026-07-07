# 配置体系设计（多微服务 / 多环境 / 变量注入 / 热更）

- 状态：设计待评审
- 日期：2026-06-03
- 适用范围：`src/common/config`、`protocal/`、`tools/`、`conf/`、构建脚本
- 关联现状：当前 `src/common/config/config.go` 为单一大 struct + 单文件加载 + 纯文本 `${ENV}` 替换；文档宣称的 `server.yaml → dev → prod` 分层覆盖**实际未实现**。本设计是配置体系的首次正式落地。

---

## 1. 目标与非目标

### 目标

1. 多微服务（gatesvr / lobbysvr / onlinesvr / matchsvr / roomsvr / scenesvr…）共享一套**通用配置**，各自再有**私有配置**。
2. 多部署环境（dev / test / prod…）的差异值集中管理，模板与取值分离。
3. 变量注入两条来源清晰分流：配置仓内部的环境差异值，与部署平台注入的密钥。
4. schema 单一事实源，可标记字段「可热更」。
5. 运行时支持热更（文件触发），读取并发安全。
6. 构建期把模板烘焙成「最终配置 + 二进制」同目录的自洽部署单元，且来源可观测。

### 非目标（本轮明确不做）

- **不做 OnReload 回调**：本轮所有热更字段均为「纯读取即生效」的参数（阈值 / 开关 / 人数）。需副作用的热更（重连 redis、改日志级别、扩容协程池）留待后续，见 §9。
- **不引入 etcd/nats 作为配置推送通道**：热更走文件 + 信号 / 轮询。
- **不做密钥加解密基础设施**（Vault / sidecar / `.enc`）：配置包只认 `${VAR}` 这个接缝，谁填由部署平台决定。
- **不引入 protoc-gen-go / protojson**：struct 由**自写 gen** 生成（读 protoc 出的 descriptor，非 protoc-gen-go 产物）；运行时 `yaml.v3` 直接解析，不经 protojson。
  - 注：gen 期**用 protoc 出 descriptor + `protoreflect` 读**（见 §6.5）——这是把「proto 解析 / 语法校验」交给 protoc，而非用它生成 Go。protoc 项目已用（`protocal/`），零新增工具链依赖。不同于现有 `gen_routes` 的「正则扫文本」，配置 gen 走结构化 descriptor，更稳。

---

## 2. 核心模型一览

| 维度 | 决策 |
|---|---|
| 目录 | `conf/`（模板，进 Git）→ build → `run/{svc}/{bin,conf,log}`（产物，不进 Git；环境由 build 脚本选 values 决定，不进路径） |
| 合并 | build 期拼 `common.yaml + <svc>.yaml`（同服务结构拼接，深合并；`<svc>` 同 key 覆盖 `common`） |
| 环境差异 | 全靠 `${value}` 占位 + `values/{env}.yaml` 填空，**无逐层深合并的环境层** |
| 变量分流 | **全小写** `${value}` → 查 `values/{env}.yaml`，**build 期填掉**；**全大写** `${VAR}` → OS 环境变量，**运行时填，渲染后保留** |
| 大小写混合 | `${Redis}`、`${redis_PWD}` 等混合大小写 → **build 报错**（无模糊地带） |
| 缺值处理 | 残留小写占位符 → **build 失败**；运行时残留大写未注入 → **启动报错**（不静默置空） |
| schema | proto 为事实源；**protoc 出 descriptor + `protoreflect` 读**（非 protoc-gen-go），自写 gen 据此生成带 `yaml:` tag 的 struct + reload/env/required 表；限定特性白名单（标量+嵌套 message+repeated） |
| proto 建模 | **嵌入式**：`Common` 消息定义一次，各服务消息内嵌它 + 私有块 |
| 静/动标记 | proto field option `[(config.reload)=true]` 标记可热更字段（留空=静态） |
| 注入标记 | proto field option `[(config.env)=true]` 标记运行时 `${VAR}` 注入字段（须 string）；与大写命名交叉校验 |
| 字段类型 | 注入字段（标 env）一律 `string` + 集中 `Validate` 解析校验；其余字段保留真实类型，原生解析 |
| 热更 | 监听 `run/{svc}/conf/config.yaml`，SIGHUP / mtime 轮询触发，atomic 快照整体替换 |
| 读取 | `config.Current()` 返回一致快照；本轮不做 OnReload 回调 |
| yaml→struct | struct 自带 `yaml:` tag，**`yaml.v3` 直接 `Unmarshal`**（原生类型，无 protojson，零新依赖） |

---

## 3. 目录结构

### 3.1 配置源（模板，进 Git）

```
conf/
├── common.yaml          # 通用结构层：redis/etcd/nats/mongodb/log/version/region/world_id/excel路径/charge_sdk...
├── gatesvr.yaml         # 各服务私有配置（可覆盖 common）
├── lobbysvr.yaml
├── onlinesvr.yaml
├── matchsvr.yaml
├── roomsvr.yaml
├── scenesvr.yaml
└── values/
    ├── dev.yaml         # dev 环境的取值（填小写 ${value}）
    ├── test.yaml
    └── prod.yaml
```

- `common.yaml` / `<svc>.yaml` 是**模板**：结构固定，环境相关的值用占位符 `${...}` 挖坑。
- `values/{env}.yaml` 是**取值表**：为小写占位符提供具体值，可取整子树或标量。
- `values/{env}.yaml` 中敏感字段（密码 / 密钥）**不写明文**，再挖一层大写环境坑 `${REDIS_PWD}`。

### 3.2 Schema 与工具（进 Git）

配置 schema 贴着配置目录放（`conf/schema/`），与业务 proto（`protocal/`）分开——`gen_routes` 只扫 `protocal/`，不会误抓配置 proto。生成的 Go 包导入路径为 `project/conf/schema/gen/config`。struct 由**自写 gen 脚本**生成（非 protoc-gen-go，见 §6.5）。

```
conf/schema/
├── config_options.proto       # 扩展 FieldOptions：reload / env
├── config_common.proto        # message Common（对应 common.yaml 结构）
├── config_<svc>.proto         # message <Svc>Config（内嵌 Common + 私有块），每服务一个
└── gen/                        # 自写 gen 脚本输出（勿手改），包 project/conf/schema/gen/config
    ├── config.go             # 带 yaml tag 的 Go struct
    └── reload_table.go       # reload / env(注入) / required(必填) 字段路径集

tools/
├── gen_routes/                # 现有（只扫 protocal/）
├── gen_config/                # 新增：protoc 出 descriptor → protoreflect 读 → struct + reload/env/required 字段表；
│                              #   强制特性白名单（标量+嵌套 message+repeated），见 §6.5
└── config_build/              # 新增：构建烘焙工具（见 §7）
```

> `conf/` 自此同时容纳「配置数据」（模板 / values）与「配置 schema 代码」（proto 源 + 生成物）；生成物性质同 `protocal/gen/`，勿手改、由命令重生成。

### 3.3 构建产物（不进 Git）

环境**不进路径**：由 build 脚本（不同环境的构建脚本选不同 `values/{env}.yaml`）决定本次烘焙用哪套取值，同一时刻 `run/` 下即一套环境的产物。换环境重新 build 即可。

```
run/
└── scenesvr/
    ├── bin/scenesvr           # 二进制
    ├── conf/config.yaml       # 烘焙后的最终配置（小写已填实，大写 ${VAR} 保留，含来源注释）
    └── log/                   # 运行时日志输出目录
```

---

## 4. 变量注入模型

### 4.1 两条来源，按大小写分流

| 占位符 | 形态 | 取值来源 | 解析时机 | 渲染后 |
|---|---|---|---|---|
| `${redis}`、`${log.default_pattern}`、`${scenesvr_cfg}` | **全小写**（含 `_` 与 `.`） | `values/{env}.yaml`，按点路径取整子树或标量 | **build 期** | 被真实值替换，占位符消失 |
| `${REDIS_PWD}`、`${WORLD_ID}` | **全大写**（含 `_`） | OS 环境变量（`os.LookupEnv`） | **运行时**（进程启动） | **原样保留**，等部署平台注入 |

> 大写约定踩在全行业惯例上：POSIX 环境变量、K8s `env`、CI/CD（GitHub Actions / GitLab CI / Jenkins）、Docker/envsubst 均以全大写命名环境变量。运维见到 `${REDIS_PWD}` 即知「部署平台注入」。

### 4.2 整子树注入（关键实现点）

现有 `expandEnv` 是**纯文本正则替换**，无法支撑 `redis: ${redis}` 这种「单行占位符 → 多行 map」的展开（文本替换会破坏缩进导致 YAML 解析失败）。

因此注入必须是**节点级**：

1. 把模板解析成 YAML 树（`yaml.v3` 的 `yaml.Node`）。
2. 遍历树，遇到值为 `${name}` 的标量节点：
   - 小写 `name`：从 `values/{env}.yaml` 树中按点路径 `name` 定位节点，**整棵子树替换**进去（标量则替换为标量）。
   - 大写 `NAME`：build 期跳过、保留；运行时按标量做 `os.LookupEnv` 文本替换。

### 4.3 嵌套占位符（先 values 后 env）

`values/{env}.yaml` 里的值可以再含大写占位符：

```yaml
# common.yaml
redis: ${redis}

# values/prod.yaml
redis:
  host: 10.43.0.44
  port: 6379
  timeout: 2
  password: ${REDIS_PWD}     # build 填 redis 子树时一并带入，大写保留到运行时
```

解析顺序固定，递归一层即可，不会无限套娃：
**① build 期用 values 填所有小写 `${value}` → ② 大写 `${VAR}` 原样保留 → ③ 运行时用 OS env 填大写。**

### 4.4 命名校验（build 强制）

- 模板 / values 出现**混合大小写**占位符（`${Redis}`、`${redis_PWD}`）→ **build 失败**。
- 模板出现小写 `${foo}` 但 `values/{env}.yaml` 查不到 `foo` → **build 失败**（拼写错 / 漏配）。
- 大写 `${FOO}` 同时在 `values` 中有定义 → **build 失败**（判别冲突，命名违规）。
- build 完成后，最终配置中**不应残留任何小写占位符**；若有 → **build 失败**。
- 运行时注入后仍残留任何 `${...}`（大写未注入）→ **启动报错**，绝不静默置空。

### 4.5 注入字段标记 `(config.env)` 与交叉校验

大写 `${VAR}` 命名是「数据侧」信号；为让「注入字段」在 **schema 侧也显式声明、可机器校验**，在 proto 字段上加 `[(config.env) = true]` 标记（与 `reload` 并列，定义见 §6.1）。两者职责不同、互相印证：

- 大写 `${VAR}`：出现在 **YAML 模板**，告诉 build「此占位符运行时填」。
- `(config.env)`：标在 **proto 字段**，告诉工具链「此字段是注入字段，类型须 `string`，且需做注入校验」。

**强制校验**（gen / build 期）：

1. 标了 `(config.env)` 的字段**必须是 `string`** 类型（环境变量本质是字符串）→ 否则 gen 报错（见 §6.5 注入字段类型约定）。
2. 模板里某字段填了大写 `${VAR}`，但其 proto 字段**未标** `(config.env)` → **build 失败**（不一致）。
3. proto 字段标了 `(config.env)`，但模板里填的是小写 `${value}` 或写死的字面量 → **build 失败**（标记与数据矛盾）。
4. 注入字段的运行时校验（存在性 / 空串 / 可解析 / 合法范围）见 §8.2，加载器据 `(config.env)` 标记自动定位这些字段，无需手列清单。

> 收益：把「注入字段必须 string + 自校验 + 注释标记」从口头约定升级为 schema 显式声明 + 机器强制；与大写命名交叉印证，抓出不一致。

---

## 5. 合并语义与时序

合并发生在 **build 期、变量填充之前**，操作对象是**仍带占位符的模板 YAML 树**。

固定三步：

```
① 拼 common.yaml + <svc>.yaml  （占位符层深合并，<svc> 同 key 覆盖 common）
② 填小写 ${value}              （来自 values/{env}.yaml，整子树或标量）
③ 保留大写 ${VAR}              （运行时由 OS env 注入）
```

### 5.1 深合并规则

- map 同名 key：递归合并。
- 标量 / 列表：`<svc>` 整体覆盖 `common`。
- **变量字段照常合并**：覆盖发生在「值还是占位符字符串」的阶段，所以 `redis: ${redis}` 被 `redis: ${room_redis}` 覆盖时，只是字符串替换，与「值是不是变量」无关；填值是其后独立的一步。

> 本设计**不提供** `_override` 整块替换指令——三层模型已用「占位符 + values 填空」取代了「逐层深合并覆盖」，无此需要。若未来确有「整块替换而非深合并」需求，再引入显式指令。

### 5.2 设计决策记录：为何 build 期拼好，而非运行时共用 common

候选两种：

- **方案 X（采用）**：build 期把 `common + <svc>` 拼成每个服务一份完整 `run/{svc}/conf/config.yaml`。
- **方案 Y（否决）**：`common.yaml` 单独放 `run/common/` 共用，各服务运行时再读两个文件合并。

选 X 的理由：

1. **部署单元自洽**：`bin + 它那份完整 config.yaml` 一个文件夹即全部真相，可单独部署 / 回滚 / diff。方案 Y 中 `run/{svc}/` 依赖 `../common/`，单独回滚一个服务时 common 版本可能对不上，产生「回滚了 scene 但 common 是别人三天前改的」类事故。
2. **运行时极简、合并风险前移**：X 让进程只读一个文件，无合并逻辑。方案 Y 把深合并 + 填值拽回运行时，§5「merge 风险只在 build 期暴露、prod 不因合并出错起不来」的好处尽失。
3. **填值时机一致**：X 下小写 `${value}` 统一 build 期填掉；方案 Y 若 common 留在 run/ 仍带 `${redis}`，填值被迫挪到运行时，烘焙模型垮掉一半。
4. **热更简单**：X 只监听一个 config.yaml；方案 Y 要同时监听 common + 私有两文件并重跑合并。

方案 X 的代价仅「common 内容在 N 份产物中物理重复」，但这是**构建产物的重复，非源的重复**（源 `conf/common.yaml` 仍唯一）。其性质等同「每个二进制都静态链接同一个库」，且 config.yaml 仅数 KB，体积淹没在数十 MB 的二进制中，在游戏服语境下不值一提。「common 改了要重刷」的真正解法是「单一源 + build 自动分发」，而非共用文件——见 §7.2 增量构建。

---

## 6. proto schema（嵌入式建模）

### 6.1 字段选项定义

```protobuf
// conf/schema/config_options.proto
syntax = "proto3";
package config;
option go_package = "project/conf/schema/gen/config";
import "google/protobuf/descriptor.proto";

extend google.protobuf.FieldOptions {
  // reload 标记该字段运行时可热更（纯读取即生效）；未标记 = 静态，启动后冻结
  bool reload   = 50101;
  // env 标记该字段运行时由大写 ${VAR} 环境注入；字段类型必须为 string（见 §6.6）
  bool env      = 50102;
  // required 标记该字段必填；加载后为零值/空串 → 报错（见 §8.2 必填校验）
  bool required = 50103;
}
```

### 6.2 通用块（单一事实源）

```protobuf
// conf/schema/config_common.proto
message Common {
  Redis  redis   = 1;
  Etcd   etcd    = 2;
  Nats   nats    = 3;
  Mongo  mongo   = 4;
  Log    log     = 5;
  string version = 6;
  string region  = 7;
  string excel_path = 8;
  // ... world_id / charge_sdk 等
}

message Redis {
  string host     = 1 [(config.required) = true];   // 必填，缺失/空 → 报错
  int32  port     = 2 [(config.required) = true];    // build 期填字面量，原生 int，必填
  int32  timeout  = 3;
  string password = 4 [(config.env) = true, (config.required) = true];  // 运行时 ${REDIS_PWD} 注入，须 string，必填
}
// Etcd / Nats / Mongo / Log 同理
```

> `port`/`timeout` 是 build 期由 `values` 填死的字面量，保留真实类型 `int32`，加载时原生解析。`password` 是运行时环境注入字段，故标 `(config.env)` 且类型为 `string`。`host`/`port`/`password` 标 `(config.required)`，缺失即报错（§8.2）。一个字段可同时带多个 option（如 `env` + `required`）。

### 6.3 服务配置（内嵌 Common + 私有块）

```protobuf
// conf/schema/config_scenesvr.proto
message SceneConfig {
  Common   common    = 1;             // 内嵌通用块
  SceneCfg scene_cfg = 2;             // 私有块
}

message SceneCfg {
  bool queue_check   = 1 [(config.reload) = true];   // 可热更
  bool disable_cs_gm = 2 [(config.reload) = true];   // 可热更
  // 未标 reload 的字段 = 静态
}
```

- 通用结构只在 `Common` 定义一次；加通用字段只改 `Common`，所有服务自动获得。
- 覆盖不影响 proto：`<svc>` 覆盖 common 的某块时，合并后的 yaml 中该块仍落在 `common.<field>` 路径下，值变结构不变。
- 访问形如 `cfg.Common.Redis`；可在 config 包提供薄 getter（如 `cfg.Redis()` 转发 `cfg.Common.Redis`）消化层级。

### 6.4 字段元数据表生成

`tools/gen_config`（同一脚本，同时产出 struct）读 protoc 出的 descriptor（§6.5），对每个配置 message 按三个 option（`proto.GetExtension` 结构化读取，非正则）各收集字段路径，生成：

```go
// reload_table.go（生成，勿手改）
package config

// ReloadableFields 配置 message 名 → 可热更字段路径集（标 reload=true）
var ReloadableFields = map[string]map[string]bool{
    "SceneConfig": {"scene_cfg.queue_check": true, "scene_cfg.disable_cs_gm": true},
}

// EnvFields 运行时 ${VAR} 注入字段（标 env=true）；驱动 §8.2-A 注入校验与 §4.5 交叉校验
var EnvFields = map[string]map[string]bool{
    "SceneConfig": {"common.redis.password": true},
}

// RequiredFields 必填字段（标 required=true）；驱动 §8.2-B 必填校验，无需手写清单
var RequiredFields = map[string]map[string]bool{
    "SceneConfig": {"common.redis.host": true, "common.redis.port": true, "common.redis.password": true},
}
```

用途：
- `ReloadableFields`：①build 期给最终 config.yaml 字段打 `[reloadable]` 注释；②运行时热更的静态字段保护（§8.4）。
- `EnvFields`：①build 期交叉校验「填 `${VAR}` 的字段必标 env」（§4.5）；②运行时注入字段集中校验（§8.2-A）。
- `RequiredFields`：运行时必填校验（§8.2-B），**自动生成、无需手写**——堵住「proto 新增字段 + 漏登记必填 + yaml 漏配 → 静默零值」的洞。

#### 标记规则（白名单 / 默认静态）

- **留空 = 静态**：默认不可热更。**不强制**写 `[(config.reload) = false]`——它与留空**完全等价**（protobuf bool option 默认值即 `false`）。规范用法是「要热更才写 `=true`，否则留空」，保持 proto 干净。
- **`= true`/`= false`/留空的语义由 descriptor 天然给出**：`gen_config` 走 `proto.GetExtension(opts, E_Reload).(bool)` 读取，留空与 `= false` 都返回 `false`、`= true` 返回 `true`——三态语义由 protobuf 运行时保证，**无需正则连值匹配**（这正是 descriptor 相对正则扫文本的稳处：正则要自己写对「连 `= true` 一起匹配」才不把 `= false` 误判为开，descriptor 不会犯这个错）。
- **静态字段自查清单**：隐式静态的代价是「漏标」与「有意静态」外观一致。为兜底，`gen_config` 在生成时**额外输出每个 message 被判定为静态的字段清单**（日志或注释形式），供作者 review 时扫一眼自查「本想热更的字段是否误落静态」。这比强制满屏 `=false` 噪音的性价比更高。

### 6.5 struct 生成方式：protoc 出 descriptor + `protoreflect` 读（非 protoc-gen-go）

不使用 `protoc-gen-go` 生成 struct（它只产 `protobuf:`/`json:` tag、无 `yaml:` tag，`yaml.v3` 直接 `Unmarshal` 时 snake_case 字段静默丢零值）。改由**自写 gen**（`tools/gen_config`）生成**带 `yaml:` tag** 的 Go struct + reload/env/required 字段表。关键在 gen 的**输入**——不再正则扫 proto 文本，而是：

```
① protoc --descriptor_set_out=conf/schema/gen/config.pb \
         --proto_path=... conf/schema/config_*.proto
   # protoc 完整解析并校验 proto（语法错在此暴露），输出 FileDescriptorSet 二进制描述符
② go run ./tools/gen_config   # 读 config.pb，用 protoreflect 遍历 message/field，生成 struct + 三张表
```

gen 读描述符、取字段信息（示意，非最终代码）：

```go
// 遍历某 message 的字段
for i := 0; i < md.Fields().Len(); i++ {
    fd := md.Fields().Get(i)
    yamlKey := string(fd.Name())                 // proto 字段名 → 原样作 yaml tag（§6.7）
    goType  := mapKind(fd)                        // fd.Kind()/Cardinality 精确给出类型、是否 repeated、是否嵌套 message
    opts    := fd.Options().(*descriptorpb.FieldOptions)
    reload  := proto.GetExtension(opts, configpb.E_Reload).(bool)    // 结构化读 option，非正则
    env     := proto.GetExtension(opts, configpb.E_Env).(bool)
    req     := proto.GetExtension(opts, configpb.E_Required).(bool)
    // ... 据此生成 struct 字段 + 累积 Reloadable/Env/Required 路径
}
```

产出与原方案完全一致——带 yaml tag 的 struct（如下）+ §6.4 三张表：

```go
// gen 输出（带 yaml tag，专为 yaml.v3 定制；勿手改）
type Redis struct {
    Host     string `yaml:"host"`
    Port     int32  `yaml:"port"`
    Timeout  int32  `yaml:"timeout"`
    Password string `yaml:"password"`
}
```

**为何走 descriptor 而非正则扫文本**（相对现有 `gen_routes` 的升级，逐项还债）：

| | 正则扫 proto 文本 | protoc descriptor + protoreflect |
|---|---|---|
| proto 语法错（漏 `;`、括号不配） | 抓不可靠（正则非解析器，§9 旧坑） | **protoc 编译期直接报错**（白送） |
| option 拼错 `(config.relod)` | 正则可能静默漏读 | **protoc 报 unknown option**（白送） |
| 字段类型 / repeated / 嵌套 | 正则抠文本，易错 | `fd.Kind()`/`Cardinality()` **精确** |
| option 值 `= true`/`= false`/留空 | 须自己写对「连值匹配」 | `proto.GetExtension` 三态天然正确（§6.4） |
| 维护 | 正则一改就崩 | 标准 API，稳 |

**依赖与工具链**：protoc 项目已用于 `protocal/`，**零新增**；gen 多依赖 `google.golang.org/protobuf/reflect/protoreflect` + `types/descriptorpb`（标准 protobuf Go 库，已在 `go.mod`）。代价仅「跑 gen 前先跑一步 protoc」，与现有 `protoc + gen_routes` 节奏一致，用一个脚本 / `make config` 串起两步。

**schema 特性白名单（gen 强制）**：为让 gen 简单可控，schema **只允许**：标量（`string`/`int32`/`int64`/`uint32`/`uint64`/`bool`/`float`/`double`）、嵌套 message、`repeated`。出现 `enum` / `oneof` / `map` / `group` 等 → gen 据 `fd.Kind()` 精确识别并**报错指出位置**，禁止 schema 膨胀到 gen 无法覆盖的复杂度。

> 取舍记录：自写 gen 是「多维护一个轻量代码生成器」换「yaml.v3 零依赖直接灌 + struct 完全掌控」；其**输入用 descriptor 而非正则**，把 proto 语法校验、option 拼写、类型识别三项从「自己写对」升级为「protoc/protoreflect 白送」，消除原正则方案的核心技术债（旧 §9.4）。仅在 schema 保持上述白名单范围内才划算；本项目 schema 简单（标量+嵌套+repeated），划算。若未来 schema 必须引入 enum/oneof/map，应重新评估改回 protoc-gen-go + `yaml.v3` 自搭 `yaml→map→json→struct` 桥的方案。

### 6.6 注入字段类型约定（string + 集中校验）

- **被 `${VAR}` 运行时注入的字段（标 `(config.env)`）一律声明 `string`**，gen 脚本强制校验：标了 `env` 却非 string → 报错（§4.5 校验 1）。
- **其余字段（build 期填死的）保留真实类型**（`int32`/`bool`/嵌套 message），`yaml.v3` 原生解析，业务直接用，零样板。
- 注入字段是少数（如 `server_id`、`password`），其「string → 目标类型」的解析与合法性判断由 config 包**集中式 `Validate`** 在加载期完成（fail-fast），解析结果供业务读强类型值，**不散落 strconv 到业务各处**。详见 §8.2。

### 6.7 yaml ↔ proto 对应规则

对应链：**proto 字段名 ↔ struct 的 `yaml:` tag ↔ yaml 的 key**，三者必须一致。`gen_config` 保证前两者一致（tag 原样取 proto 字段名），schema/yaml 作者保证 yaml key 与 proto 字段名一致。对标行业：Envoy（proto 配置）、k8s（tag 对应）同款骨架。

| proto | Go struct（gen 产出） | yaml |
|---|---|---|
| 字段名 `pool_size`（snake_case） | `PoolSize int32 \`yaml:"pool_size"\`` | `pool_size: 50` |
| 嵌套 message 字段 `redis`（**字段名**非类型名） | `Redis *Redis \`yaml:"redis"\`` | `redis:` 缩进子块 |
| `repeated string endpoints` | `Endpoints []string \`yaml:"endpoints"\`` | `endpoints:` 列表 |
| `string`/`int*`/`bool`/`float`/`double` | 对应 Go 原生类型 | 标量（数字/bool 无引号） |

要点：

- **命名**：proto3 字段名用 `snake_case`，yaml key 同名直接相等；gen 把 proto 字段名**原样**写入 `yaml:` tag（这正是自写 gen 的核心职责——架起 Go `CamelCase` 与 yaml `snake_case` 的桥）。
- **嵌套**：yaml 的 key 是 message 类型的**字段名**（如 `redis`），不是类型名（如 `Redis`）。嵌套式建模（§6.3）下 yaml 顶层含 `common:` 层。
- **对应校验的对象是填值后的最终 config.yaml**，不是带占位符的模板——`${...}` 在解析前已填实（§8.1），proto/struct 看不到占位符。
- **防「静默不对应」**：`yaml.v3` 默认对「多余/拼错 key」「缺失 key」均不报错（前者忽略、后者留零值）。两道防线见 §8.1（严格模式）与 §8.2（必填校验），缺一不可——拼错与漏配的报错信息不同，都要抓。

---

## 7. build 脚本职责（`tools/config_build`，Go）

输入：`conf/` 模板 + 目标服务 + 目标环境 + 二进制路径。
输出：`run/{svc}/{bin,conf/config.yaml,log}`（环境由本次 build 选用的 `values/{env}.yaml` 决定，不进路径）。

步骤：

1. **加载模板**：解析 `common.yaml` + `<svc>.yaml` 为 YAML 树。
2. **深合并**（§5.1）：`<svc>` 覆盖 `common`，得占位符层合并树。
3. **命名校验**（§4.4）：扫所有占位符，混合大小写 / 小写无对应 values / 大写撞 values → 报错。
4. **填小写 `${value}`**（§4.2）：从 `values/{env}.yaml` 节点级注入（整子树 / 标量）。
5. **保留大写 `${VAR}`**：跳过不处理。
6. **残留校验**：合并树中若仍有小写占位符 → 报错。
7. **生成带注释的最终 config.yaml**（§7.1）。
8. **拷贝二进制**到 `run/{svc}/bin/`，建空 `log/` 目录。

### 7.1 来源注释（逐字段）

注释固定字段顺序，机器可生成、人可扫、可被工具再解析：

```
# [reloadable] [override ← <来源文件> (原值)] [from <取值来源>]
```

- `[reloadable]`：该字段在 proto 中标了 `[(config.reload)=true]` 才出现（查 `ReloadableFields`）。
- `[override ← <svc>.yaml (common: <原值>)]`：`<svc>` 覆盖了 common 才出现，带原值。
- `[from values/{env}.yaml]` / `[env, runtime]`：值的最终来源。

示例：

```yaml
common:
  redis:                     # override ← scenesvr.yaml (common: ${redis})
    host: 10.43.0.44         # from values/prod.yaml (key: scene_redis)
    port: 6379               # from values/prod.yaml
    password: ${REDIS_PWD}   # env, runtime
  log:
    base:
      pattern: "..."         # from values/prod.yaml (key: log.default_pattern)
scene_cfg:
  queue_check: false         # reloadable | from values/prod.yaml
  disable_cs_gm: false       # reloadable | from values/prod.yaml
```

> 注：`[override ← ...]` 仅出现在「`<svc>` 覆盖了 `common` 同 key」的字段上（如上例 `common.redis` 被 `scenesvr.yaml` 覆盖）。服务私有块（`scene_cfg`）的字段不存在覆盖 common 的情况，故无 `override` 段。

> 实现要求：注释必须**精确挂到每个字段**，故 build 不能整树 marshal 一把出去，须用 `yaml.v3` 的 `yaml.Node` 树挂 `HeadComment` / `LineComment` 后再 marshal。

### 7.2 构建范围与增量构建（CLI）

build 工具支持选择构建范围，回应「common 改了要重刷全部服务」的诉求——靠「单一源 + 一键全刷 / 增量」，而非共用文件（见 §5.2）。

环境是 build 的**输入**（决定填哪套 `values/{env}.yaml`），但**不进产物路径**——通常由不同环境的构建脚本各自指定 `--env`，产物统一落 `run/{svc}/`。CLI 形态（最终 flag 名实现时定，语义如下）：

| 用法 | 行为 |
|---|---|
| `config_build --env prod --svc scenesvr` | 用 prod values 只构建单个服务 |
| `config_build --env prod --all` | 用 prod values 构建**全部**服务（common 改动后一键全刷） |
| `config_build --env prod --all --incremental` | 仅重建「相关源较产物更新」的服务（见下） |

**增量判定**：某服务 `run/{svc}/conf/config.yaml` 的 mtime 早于其任一**输入源**的 mtime，则需重建。输入源 = `conf/common.yaml` + `conf/{svc}.yaml` + `conf/values/{env}.yaml` + 相关 `conf/schema/config_*.proto`（reload 表变化也影响注释）。

- 改 `common.yaml` → 增量模式下**所有**服务都判定为脏（common 是所有人的输入）→ 全部重建，符合预期。
- 改单个 `<svc>.yaml` → 只该服务重建。
- 增量是**纯构建期优化**，不改变任何产物内容或运行时行为；`--all`（非增量）始终是安全的全量兜底。

> 实现注意：①增量靠 mtime 是「够用」的工程取舍（与现有工具链一致），不追求内容哈希级精确。CI 全量构建应走 `--all` 不加 `--incremental`，避免缓存误判。②`run/{svc}/` 不带 env，故**切换环境重 build 前应清空或覆盖 `run/`**，避免上一环境的残留产物与新环境混淆。

---

## 8. 运行时加载与热更（`src/common/config`）

### 8.1 加载流程

```
读 run/{svc}/conf/config.yaml
  → 填大写 ${VAR}（os.LookupEnv，节点级；未注入或空串则报错）
  → yaml.v3 严格模式 Decode 到自写 gen 的 <Svc>Config struct（带 yaml tag，原生类型）
  → Validate：注入字段（string）集中解析+校验 + 必填/范围业务校验
  → 存入 atomic.Pointer[<Svc>Config] 快照
```

> **yaml→struct 实现要点**：struct 由自写 gen 脚本生成、**自带 `yaml:` tag**（§6.5），故 `yaml.v3` 可**直接 `Decode`** 一行命中，原生解析 `int`/`bool`，无需 protojson、无需 `yaml→json` 中转、零新依赖。注入字段为 `string`（§6.6），在 `yaml.v3` 下永远最稳。

> **严格模式（防 yaml 拼错/多余 key）**：用 `yaml.NewDecoder` + `dec.KnownFields(true)`，config.yaml 出现 struct 无对应字段的 key（如把 `pool_size` 拼成 `pool_sze`）→ **加载报错**「unknown field」，而非静默忽略留零值。config.yaml 是烘焙后的纯数据（`_override` 等合并指令已在 build 期剥除），适合开严格模式。它抓「多余/拼错」，与 §8.2 的必填校验（抓「缺失」）互补，缺一不可。

### 8.2 集中校验（`Validate`）：注入字段解析 + 必填 + 范围

加载与 reload 都在 Decode 后调用 `Validate`，**加载期 fail-fast**，错误一次性暴露，不拖到运行时。三部分：

**A. 注入字段解析校验**（标了 `(config.env)` 的 string 字段，gen 出的注入字段清单驱动，无需手列）：

1. **存在性**：值仍是 `${VAR}` 字面量（未注入）或**空串** → 报错。空串视为「未提供」，不是合法值（如密码为空最危险）。
2. **可解析性**：若逻辑类型是数字（如 `server_id`），`strconv.Atoi` 失败 → 报错并指明字段。
3. **合法范围**：转换成功后再校验范围（端口 1–65535、id > 0 等）。
4. **缓存强类型结果**：解析结果存入伴生字段/快照，业务读已解析的强类型值，**不在业务各处散落 strconv**。

**B. 必填校验**（防 yaml 缺 key）：严格模式抓不到「缺失」——缺 key 时 yaml 没那一行、不算未知字段，字段只留零值。故对必填字段显式查零值/空串，缺失 → 报错。**必填清单由 gen 生成的 `RequiredFields` 驱动**（标 `[(config.required)=true]` 的字段，§6.4），**不手写**——这样「proto 新增必填字段」自动纳入校验，杜绝漏登记。与 §8.1 严格模式互补：严格模式抓「拼错/多余」，必填校验抓「漏配」；二者报错信息不同（前者「unknown field hsot」直指拼写，后者「host 必填」），都要有。

> 实现注意：必填校验按 `RequiredFields` 的字段路径在解析后的 struct 上取值判零。零值判定需区分类型——string 判空串、数字判 0、嵌套 message 判 nil、repeated 判空。对「0 是合法值」的数字字段不应标 required（否则误报），此类字段的"必须显式配置"语义靠业务范围校验（C）表达。

**C. 范围/业务校验**：非注入的真实类型字段，按业务规则校验取值合理性（枚举值合法、数值区间、依赖关系等）。

### 8.3 读取 API

```go
// 返回当前一致快照；读期间不会被替换（atomic.Pointer）
func Current() *SceneConfig

// 业务读取：cfg := config.Current(); _ = cfg.SceneCfg.QueueCheck
```

- 快照整体不可变：reload 时构造**新** struct，atomic 整体替换指针；持有旧快照的 goroutine 继续读旧值，无撕裂。
- 本轮**不提供** OnReload 回调（§9）。

### 8.4 热更触发与生效

- **触发源**：监听 `run/{svc}/conf/config.yaml`——SIGHUP 信号，或定时 stat mtime 轮询（二选一或都支持，实现时定）。
- **生效**：重跑 §8.1 流程（含 §8.2 校验）构造新快照 → atomic 替换。
- **静态字段保护**：reload 解析出的新快照与当前快照逐字段比对，凡**未标 `[(config.reload)=true]` 的字段发生了变化 → 拒绝本次 reload 并告警**（静态字段如监听端口、NodeID 误改不生效，避免诡异行为）。仅当全部差异字段都在 `ReloadableFields` 内，才接受替换。
- reload 失败（解析错 / 校验错 / 改了静态字段）→ **保留旧快照**，记录错误日志，不中断服务。

---

## 9. 已知限制与后续扩展点

1. **无 OnReload 回调**：本轮热更仅支持「纯读取即生效」字段。一旦要热更需副作用的配置（redis 地址重连、日志级别、连接池扩容），须补订阅机制（建议复用 `src/common/event` 的泛型 Bus，reload 后发布事件，各模块订阅执行重建动作）。
2. **源漂移**：热更对象是 `run/{svc}/conf/config.yaml`（产物），不是 `conf/` 源模板。线上手改 config.yaml 热更生效后，`conf/` 源未变，下次 build 会覆盖回去。**约束：线上热改后必须回写源模板。**
3. **build 期 merge 风险前移**：合并逻辑只在 build 期跑，不在生产进程；merge bug 在构建期暴露，prod 不会因合并出错起不来——这是优点，但要求 build 工具有充分测试。
4. **gen 依赖 descriptor 两步走**：`gen_config` 走「protoc 出 descriptor → protoreflect 读」（§6.5），proto 语法错与 option 拼写错由 protoc 在第一步暴露（相对现有 `gen_routes` 正则方案的改进，已消除原正则的「语法错抓不可靠」债）。代价是构建链多一步 protoc，须用脚本 / `make config` 把两步串牢，避免「只跑了 gen 没跑 protoc、descriptor 过期」。特性仍限白名单内（标量+嵌套 message+repeated，§6.5）；若未来 schema 必须引入 enum/oneof/map，须重新评估改回 protoc-gen-go + `yaml.v3` 自搭 `yaml→map→json→struct` 桥。

---

## 10. 对现有代码的影响

- `src/common/config/config.go`：**整个旧实现（单 struct + 单文件加载 + 文本 `${ENV}` 替换）删除重写**，无可复用部分。新实现按「读最终 config.yaml + 节点级大写注入 + `yaml.v3` 直接 Unmarshal 到自写 gen struct + 集中 Validate + atomic 快照 + 热更」，怎么简单怎么来。需评估各服务 `main.go` 的加载调用改动。
- 现有 `conf/server.yaml`、`server.dev.yaml`、`server.prod.yaml`、`lobby.yaml`、`router.yaml`、`online.yaml`：迁移为 `common.yaml` + 各 `<svc>.yaml` + `values/{env}.yaml`。`server.prod.yaml`（当前未被加载）随之退役。
- 各服务 `main.go`：`config.MustLoad("conf/xxx.yaml")` → 加载约定的 `run/{svc}/conf/config.yaml`（或由框架按服务名定位）。
- 文档同步：`architecture.md` §配置、`development.md` §新增配置、`CLAUDE.md` 索引需按本设计更新（项目维护约定要求）。

---

## 11. 待实现工作清单（供 writing-plans 细化）

1. `conf/schema/config_options.proto`（reload + env option）+ `config_common.proto` + 各 `config_<svc>.proto`（包 `project/conf/schema/gen/config`）。
2. `tools/gen_config`：①`protoc --descriptor_set_out` 把 `conf/schema/config_*.proto` 编成 descriptor（语法/option 拼写错在此暴露）；②读 descriptor、用 `protoreflect` 遍历生成带 `yaml:` tag 的 struct + `reload_table.go`（`ReloadableFields`/`EnvFields`/`RequiredFields` 三张表，option 经 `proto.GetExtension` 结构化读）；强制特性白名单（标量+嵌套 message+repeated，据 `fd.Kind()` 否则报错）；env 字段须 string 否则报错；输出静态字段自查清单（§6.4/6.5）。两步用脚本 / `make config` 串牢。
3. `tools/config_build`：模板加载 / 深合并 / 命名校验 / `(config.env)` 交叉校验（§4.5）/ 小写填值 / 残留校验 / 带注释最终 config.yaml / 拷贝产物到 `run/{svc}/`；CLI 支持 `--env` / `--svc` / `--all` / `--incremental`（§7.2）。
4. `src/common/config` **删除旧实现重写**：节点级大写 `${VAR}` 注入（空串=缺失）+ `yaml.v3` 严格模式 Decode + 集中 `Validate`（注入解析 / `RequiredFields` 必填 / 范围，§8.2）+ atomic 快照 + 热更（信号/轮询）+ 静态字段保护。
5. `conf/` 模板迁移：common + 各 svc + values/{dev,test,prod}。
6. 各服务 `main.go` 接入新加载路径。
7. 测试：合并、命名校验、`(config.env)` 交叉校验、整子树注入、嵌套大写保留、缺值/空串报错、注入字段解析校验、**严格模式拒未知/拼错 key**、**`RequiredFields` 必填校验拒漏配（含新增字段自动纳入）**、yaml↔struct 对应（嵌套/repeated/类型）、静态字段保护、热更快照替换、特性白名单拒绝 enum/oneof/map；**`gen_config` 自身单测**（给定小 proto，断言生成的 struct 文本 + 三张表内容 + 白名单越界/env 非 string 时报错——生成逻辑的正确性自身需被测，descriptor 只保证输入可靠，不保证拼 struct 的代码无 bug）。
8. 文档同步：`architecture.md` / `development.md` / `CLAUDE.md`。
