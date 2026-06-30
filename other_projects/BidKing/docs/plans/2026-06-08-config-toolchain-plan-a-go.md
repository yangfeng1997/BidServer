# 配置工具链重设计 — 计划 A（Go 侧）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完成 Go 侧全部改动：proto schema 重命名+结构重构、gen_config 更新、config_build 渲染逻辑变更、loader 双文件加载、所有服务 main.go 更新，使整个系统在新的目录与结构约定下正常运行。

**Architecture:** proto 文件改名并去除中间 message 层（`GateCfg`→直接字段在 `GateSvr`），Common 与服务私有配置彻底分离，各自独立渲染到 `run/common/` 和 `run/<svr>/`，loader 分两次加载，框架层统一加载 common，服务只加载私有配置。

**Tech Stack:** Go 1.21+、protoc、`gopkg.in/yaml.v3`、`google.golang.org/protobuf`

**执行前提：** 在 `docs/config-toolchain-redesign` 分支上操作（已有设计文档）。

**计划 B 依赖本计划完成**，B 负责 Python 三脚本。

---

## 文件变更地图

### 新建
- `conf/schema/types.proto` — `SvrMeta` message
- `conf/schema/options.proto` — 从 `config_options.proto` 重命名
- `conf/schema/common.proto` — 从 `config_common.proto` 重命名，message 改名 `CommonConfig`
- `conf/schema/gatesvr.proto` — 从 `config_gatesvr.proto` 重命名，结构重构
- `conf/schema/lobbysvr.proto` — 从 `config_lobbysvr.proto` 重命名，结构重构
- `conf/schema/onlinesvr.proto` — 从 `config_onlinesvr.proto` 重命名，结构重构
- `conf/schema/routersvr.proto` — 从 `config_routersvr.proto` 重命名，结构重构

### 删除（由新文件替代）
- `conf/schema/config_options.proto`
- `conf/schema/config_common.proto`
- `conf/schema/config_gatesvr.proto`
- `conf/schema/config_lobbysvr.proto`
- `conf/schema/config_onlinesvr.proto`
- `conf/schema/config_routersvr.proto`

### 修改
- `tools/gen_config/main.go` — 跳过规则改为只跳过 `options.proto`；默认 descriptor 路径改为 `config.pb.descriptor`；包名输出改为 `conf`
- `tools/gen_config/gen_config_test.go` — 3 处 `config.pb` → `config.pb.descriptor`；proto 内容对齐新包名/文件名
- `conf/schema/gen/config.go` — 由 gen_config 重新生成（包名 `conf`，新 struct 名）
- `conf/schema/gen/reload_table.go` — 由 gen_config 重新生成
- `conf/schema/gen/config.pb` → `conf/schema/gen/config.pb.descriptor` — 重命名
- `conf/common.yaml` — 只含 common 字段，去掉服务私有字段
- `conf/gatesvr.yaml` — 只含 GateSvr 私有字段（含 meta），去掉 common
- `conf/lobbysvr.yaml` — 同上
- `conf/onlinesvr.yaml` — 同上
- `conf/routersvr.yaml` — 同上
- `conf/values/dev.yaml` — 新增 `svr_list`
- `conf/values/prod.yaml` — 新增 `svr_list`
- `tools/config_build/main.go` — 移除深合并逻辑，新增 common 独立渲染，`--svc=common` 渲染 common
- `tools/config_build/build_test.go` — 更新测试 YAML 结构
- `src/common/config/loader.go` — 新增 `LoadCommon()`；泛型 `Load[T]()` 独立函数；包名引用从 `genconfig` 改为 `conf`
- `src/common/config/loader_test.go` — 全量更新：分离 common/svr YAML，新 struct 名
- `src/servers/gatesvr/main.go` — 改用双 loader 加载
- `src/servers/lobbysvr/main.go` — 同上
- `src/servers/onlinesvr/main.go` — 同上
- `src/servers/routersvr/main.go` — 同上
- `development.md` — 更新命令示例中的 descriptor 文件名和 proto 文件名
- `architecture.md` — 同上

---

### Task 1: 新建 proto schema 文件（重命名+重构）

**Files:**
- Create: `conf/schema/options.proto`
- Create: `conf/schema/types.proto`
- Create: `conf/schema/common.proto`
- Create: `conf/schema/gatesvr.proto`
- Create: `conf/schema/lobbysvr.proto`
- Create: `conf/schema/onlinesvr.proto`
- Create: `conf/schema/routersvr.proto`

- [ ] **Step 1: 新建 `conf/schema/options.proto`**

```proto
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "google/protobuf/descriptor.proto";

extend google.protobuf.FieldOptions {
  bool reload   = 50101;
  bool env      = 50102;
  bool required = 50103;
}
```

- [ ] **Step 2: 新建 `conf/schema/types.proto`**

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

- [ ] **Step 3: 新建 `conf/schema/common.proto`**

```proto
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "conf/schema/options.proto";

message CommonConfig {
  Redis  redis      = 1;
  Etcd   etcd       = 2;
  Nats   nats       = 3;
  Mongo  mongo      = 4;
  Log    log        = 5;
  string version    = 6;
  string region     = 7;
  string excel_path = 8;
}

message Redis {
  string host     = 1 [(conf.required) = true];
  int32  port     = 2 [(conf.required) = true];
  int32  timeout  = 3;
  string password = 4 [(conf.env) = true, (conf.required) = true];
}

message Etcd {
  repeated string endpoints         = 1 [(conf.required) = true];
  int32           lease_ttl         = 2;
  int32           dial_timeout_sec  = 3;
  int32           sync_interval_sec = 4;
  int32           max_retries       = 5;
  string          key_prefix        = 6;
  int32           shutdown_delay_ms = 7;
}

message Nats {
  repeated string urls              = 1 [(conf.required) = true];
  int32           reconnect_wait_ms = 2;
}

message Mongo {
  string uri      = 1 [(conf.env) = true, (conf.required) = true];
  string database = 2;
}

message Log {
  string level = 1 [(conf.reload) = true];
  string dir   = 2;
}
```

- [ ] **Step 4: 新建 `conf/schema/gatesvr.proto`**

```proto
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "conf/schema/types.proto";
import "conf/schema/options.proto";

message GateSvr {
  SvrMeta meta                = 1;
  string  node_id             = 2 [(conf.required) = true];
  string  server_type_name    = 3 [(conf.required) = true];
  string  addr                = 4 [(conf.required) = true];
  int32   heartbeat_sec       = 5 [(conf.reload) = true];
  int32   shutdown_timeout_sec = 6;
}
```

- [ ] **Step 5: 新建 `conf/schema/lobbysvr.proto`**

```proto
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "conf/schema/types.proto";
import "conf/schema/options.proto";

message LobbySvr {
  SvrMeta meta             = 1;
  string  node_id          = 2 [(conf.required) = true];
  string  server_type_name = 3 [(conf.required) = true];
  string  addr             = 4 [(conf.required) = true];
}
```

- [ ] **Step 6: 新建 `conf/schema/onlinesvr.proto`**

```proto
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "conf/schema/types.proto";
import "conf/schema/options.proto";

message OnlineSvr {
  SvrMeta meta             = 1;
  string  node_id          = 2 [(conf.required) = true];
  string  server_type_name = 3 [(conf.required) = true];
  string  addr             = 4 [(conf.required) = true];
}
```

- [ ] **Step 7: 新建 `conf/schema/routersvr.proto`**

```proto
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "conf/schema/types.proto";
import "conf/schema/options.proto";

message RouterSvr {
  SvrMeta meta             = 1;
  string  node_id          = 2 [(conf.required) = true];
  string  server_type_name = 3 [(conf.required) = true];
  string  addr             = 4 [(conf.required) = true];
}
```

- [ ] **Step 8: 删除旧 proto 文件**

```bash
git rm conf/schema/config_options.proto
git rm conf/schema/config_common.proto
git rm conf/schema/config_gatesvr.proto
git rm conf/schema/config_lobbysvr.proto
git rm conf/schema/config_onlinesvr.proto
git rm conf/schema/config_routersvr.proto
```

注意：`config_matchsvr.proto` 和 `config_roomsvr.proto` 暂不在本次改动范围（matchsvr/roomsvr 未列入 svr_list），保留原文件不动。

- [ ] **Step 9: Commit**

```bash
git add conf/schema/
git commit -m "refactor: proto schema 重命名并重构（Common/服务私有彻底分离）"
```

---

### Task 2: 更新 gen_config 工具

**Files:**
- Modify: `tools/gen_config/main.go`
- Modify: `tools/gen_config/gen_config_test.go`

- [ ] **Step 1: 更新 `main.go` 默认 descriptor 路径和包名输出**

将 `main()` 中的 flag 默认值从 `config.pb` 改为 `config.pb.descriptor`：

```go
pbPath := flag.String("pb", "conf/schema/gen/config.pb.descriptor", "protoc 输出的 FileDescriptorSet 二进制路径")
```

- [ ] **Step 2: 更新 `generate()` 中跳过规则**

在 `generate()` 函数的 `files.RangeFiles` 里，将跳过判断从：

```go
if path.Base(p) == "config_options.proto" {
    return true
}
```

改为：

```go
if path.Base(p) == "options.proto" {
    return true
}
```

`types.proto` 不跳过，其中的 `SvrMeta` 需要生成 struct。

- [ ] **Step 3: 更新 `genStructFile()` 和 `genTableFile()` 的包名输出**

将两个函数中的：

```go
buf.WriteString("package config\n\n")
```

均改为：

```go
buf.WriteString("package conf\n\n")
```

- [ ] **Step 4: 更新 `gen_config_test.go` 中 3 处 `config.pb` 路径**

将测试文件中所有：

```go
pbOut := filepath.Join(dir, "config.pb")
```

改为：

```go
pbOut := filepath.Join(dir, "config.pb.descriptor")
```

同时将测试用 proto 内容中的 `package config` 改为 `package conf`，`go_package` 改为 `"project/conf/schema/gen"`，import 改为 `"options.proto"`，option 前缀从 `config.` 改为 `conf.`：

```go
os.WriteFile(optProto, []byte(`
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "google/protobuf/descriptor.proto";
extend google.protobuf.FieldOptions {
  bool reload   = 50101;
  bool env      = 50102;
  bool required = 50103;
}
`), 0644)

os.WriteFile(svcProto, []byte(`
syntax = "proto3";
package conf;
option go_package = "project/conf/schema/gen";
import "options.proto";
message TestSvcConfig {
  string host     = 1 [(conf.required) = true];
  int32  port     = 2 [(conf.required) = true];
  string password = 3 [(conf.env) = true, (conf.required) = true];
  bool   debug    = 4 [(conf.reload) = true];
}
`), 0644)
```

- [ ] **Step 5: 运行 gen_config 测试（需安装 protoc）**

```bash
go test ./tools/gen_config/... -v -run TestGenStruct
```

若本机无 protoc，跳过（测试会自动 Skip）：

```
--- SKIP: TestGenStruct (protoc not found, skipping gen integration test)
```

- [ ] **Step 6: Commit**

```bash
git add tools/gen_config/
git commit -m "refactor: gen_config 更新跳过规则、包名输出、descriptor 默认路径"
```

---

### Task 3: 重新生成 conf/schema/gen/

**Files:**
- Modify: `conf/schema/gen/config.go` （由 gen_config 生成）
- Modify: `conf/schema/gen/reload_table.go` （由 gen_config 生成）
- Rename: `conf/schema/gen/config.pb` → `conf/schema/gen/config.pb.descriptor`

- [ ] **Step 1: 运行 protoc 生成新 descriptor（需安装 protoc）**

```bash
protoc --proto_path=. \
       --descriptor_set_out=conf/schema/gen/config.pb.descriptor \
       --include_imports \
       conf/schema/options.proto \
       conf/schema/types.proto \
       conf/schema/common.proto \
       conf/schema/gatesvr.proto \
       conf/schema/lobbysvr.proto \
       conf/schema/onlinesvr.proto \
       conf/schema/routersvr.proto
```

预期：生成 `conf/schema/gen/config.pb.descriptor`，无报错。

- [ ] **Step 2: 运行 gen_config 生成 Go 代码**

```bash
go run ./tools/gen_config --pb=conf/schema/gen/config.pb.descriptor --out=conf/schema/gen
```

预期：`conf/schema/gen/config.go` 和 `conf/schema/gen/reload_table.go` 被覆盖，包名为 `conf`。新 struct 包含：`CommonConfig`、`Redis`、`Etcd`、`Nats`、`Mongo`、`Log`、`SvrMeta`、`GateSvr`、`LobbySvr`、`OnlineSvr`、`RouterSvr`。

- [ ] **Step 3: 删除旧 config.pb**

```bash
git rm conf/schema/gen/config.pb
```

- [ ] **Step 4: 验证生成的 config.go 包名和结构**

```bash
head -5 conf/schema/gen/config.go
grep -E "^type (GateSvr|LobbySvr|OnlineSvr|RouterSvr|SvrMeta|CommonConfig) struct" conf/schema/gen/config.go
```

预期输出包含：
```
package conf

type CommonConfig struct {
type GateSvr struct {
type LobbySvr struct {
type OnlineSvr struct {
type RouterSvr struct {
type SvrMeta struct {
```

- [ ] **Step 5: Commit**

```bash
git add conf/schema/gen/
git commit -m "chore: 重新生成 conf/schema/gen（新包名 conf，新 struct）"
```

---

### Task 4: 更新 conf/ yaml 模板

**Files:**
- Modify: `conf/common.yaml`
- Modify: `conf/gatesvr.yaml`
- Modify: `conf/lobbysvr.yaml`
- Modify: `conf/onlinesvr.yaml`
- Modify: `conf/routersvr.yaml`
- Modify: `conf/values/dev.yaml`
- Modify: `conf/values/prod.yaml`

- [ ] **Step 1: 更新 `conf/common.yaml`（只保留 common 字段，去掉服务私有内容）**

```yaml
# conf/common.yaml — CommonConfig 模板；${lowercase} = values 填充，${UPPER} = 运行时注入
redis:
  host: ${redis_host}
  port: ${redis_port}
  timeout: 2
  password: ${REDIS_PWD}
etcd:
  endpoints: ${etcd_endpoints}
  lease_ttl: 10
  dial_timeout_sec: 5
  sync_interval_sec: 30
  max_retries: 10
  key_prefix: "nodes/"
  shutdown_delay_ms: 300
nats:
  urls: ${nats_urls}
  reconnect_wait_ms: 1000
mongo:
  uri: ${MONGO_URI}
  database: game
log:
  level: info
  dir: ./logs
version: "1.0.0"
region: "default"
excel_path: ./data/excel
```

- [ ] **Step 2: 更新 `conf/gatesvr.yaml`（只含 GateSvr 私有字段）**

```yaml
# conf/gatesvr.yaml — GateSvr 私有配置模板
meta:
  pre_application_list: []
  pre_application_path: ./bin
  pre_mine_application_list: []
node_id: ${gate_node_id}
server_type_name: gatesvr
addr: ${gate_addr}
heartbeat_sec: 30
shutdown_timeout_sec: 10
```

- [ ] **Step 3: 更新 `conf/lobbysvr.yaml`**

```yaml
# conf/lobbysvr.yaml — LobbySvr 私有配置模板
meta:
  pre_application_list: []
  pre_application_path: ./bin
  pre_mine_application_list: []
node_id: ${lobby_node_id}
server_type_name: lobbysvr
addr: ${lobby_addr}
```

- [ ] **Step 4: 更新 `conf/onlinesvr.yaml`**

```yaml
# conf/onlinesvr.yaml — OnlineSvr 私有配置模板
meta:
  pre_application_list: []
  pre_application_path: ./bin
  pre_mine_application_list: []
node_id: ${online_node_id}
server_type_name: onlinesvr
addr: ${online_addr}
```

- [ ] **Step 5: 更新 `conf/routersvr.yaml`**

```yaml
# conf/routersvr.yaml — RouterSvr 私有配置模板
meta:
  pre_application_list: []
  pre_application_path: ./bin
  pre_mine_application_list: []
node_id: ${router_node_id}
server_type_name: routersvr
addr: ${router_addr}
```

- [ ] **Step 6: 更新 `conf/values/dev.yaml`（新增 svr_list）**

```yaml
# conf/values/dev.yaml — dev 环境取值
svr_list:
  - gatesvr
  - lobbysvr
  - onlinesvr
  - routersvr
redis_host: "127.0.0.1"
redis_port: 6379
etcd_endpoints:
  - "localhost:2379"
nats_urls:
  - "nats://localhost:4222"
gate_node_id: "1.1.1"
gate_addr: "0.0.0.0:8888"
lobby_node_id: "1.2.1"
lobby_addr: "0.0.0.0:8801"
online_node_id: "1.5.1"
online_addr: "0.0.0.0:8851"
router_node_id: "1.6.1"
router_addr: "0.0.0.0:8861"
```

- [ ] **Step 7: 更新 `conf/values/prod.yaml`（新增 svr_list）**

在 `conf/values/prod.yaml` 顶部 `redis_host:` 之前插入：

```yaml
svr_list:
  - gatesvr
  - lobbysvr
  - onlinesvr
  - routersvr
```

- [ ] **Step 8: Commit**

```bash
git add conf/
git commit -m "refactor: conf yaml 模板拆分（common 独立，各服只含私有字段）"
```

---

### Task 5: 更新 config_build 渲染逻辑

**Files:**
- Modify: `tools/config_build/main.go`
- Modify: `tools/config_build/build_test.go`

- [ ] **Step 1: 先写失败测试——common 独立渲染**

在 `tools/config_build/build_test.go` 中新增测试，验证 `build(cfg)` 在 `svc="common"` 时只输出 common 字段，不含 `node_id` 等服务字段：

```go
func TestBuild_CommonOnly(t *testing.T) {
    dir := t.TempDir()
    // 写 common.yaml（只含公共字段）
    os.MkdirAll(filepath.Join(dir, "values"), 0755)
    os.WriteFile(filepath.Join(dir, "common.yaml"), []byte(`
redis:
  host: ${redis_host}
  port: ${redis_port}
log:
  level: info
`), 0644)
    os.WriteFile(filepath.Join(dir, "values", "dev.yaml"), []byte(`
redis_host: "127.0.0.1"
redis_port: 6379
`), 0644)

    outDir := filepath.Join(t.TempDir(), "common", "conf")
    cfg := buildConfig{
        confDir:   dir,
        svc:       "common",
        env:       "dev",
        valuesDir: filepath.Join(dir, "values"),
        outDir:    outDir,
    }
    if err := build(cfg); err != nil {
        t.Fatalf("build common failed: %v", err)
    }
    out, _ := os.ReadFile(filepath.Join(outDir, "config.yaml"))
    if !strings.Contains(string(out), "127.0.0.1") {
        t.Errorf("expected redis host in output, got:\n%s", out)
    }
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./tools/config_build/... -v -run TestBuild_CommonOnly
```

预期：编译通过，测试 FAIL（目前 common 渲染走的是正常服务流程，没有问题但让我们验证路径正确）。若 PASS 也可接受（说明现有逻辑已能处理 svc=common 情形）。

- [ ] **Step 3: 修改 `main.go`——`listServices` 不再排除 common 渲染，但 `--all` 时改为显式列举**

`main()` 中 `--all` 模式新增 common 服务，`listServices` 保持排除 `common.yaml`（common 由 `--all` 显式首个添加）：

将 `main()` 中 `*all` 分支改为：

```go
if *all {
    names, err := listServices(*confDir)
    if err != nil {
        fmt.Fprintf(os.Stderr, "config_build 列举服务失败: %v\n", err)
        os.Exit(1)
    }
    // common 始终作为第一个渲染，输出到 run/common/conf/
    svcs = append([]string{"common"}, names...)
} else {
    if *svc == "" {
        fmt.Fprintln(os.Stderr, "config_build: 需指定 --svc 或 --all")
        os.Exit(1)
    }
    svcs = []string{*svc}
}
```

`outDir` 计算不变（`run/common/conf/` 由 `filepath.Join(*runDir, "common", "conf")` 自然产生）。

- [ ] **Step 4: 修改 `needsRebuild`——svc=common 时，只有 common.yaml 是输入源**

在 `needsRebuild` 函数中，将源文件列表改为：

```go
var srcs []string
if svc == "common" {
    srcs = []string{
        filepath.Join(confDir, "common.yaml"),
        filepath.Join(valuesDir, env+".yaml"),
    }
} else {
    srcs = []string{
        filepath.Join(confDir, svc+".yaml"),
        filepath.Join(valuesDir, env+".yaml"),
    }
}
```

注意：服务渲染不再读 `common.yaml`，所以 `common.yaml` 从服务的输入源中移除。

- [ ] **Step 5: 修改 `build` 函数——服务渲染不再合并 common**

将 `build()` 函数中的逻辑改为：

```go
func build(cfg buildConfig) error {
    var tree *yaml.Node
    var err error

    if cfg.svc == "common" {
        // common 单独渲染：只加载 common.yaml
        tree, err = loadYAMLNode(filepath.Join(cfg.confDir, "common.yaml"))
        if err != nil {
            return fmt.Errorf("加载 common.yaml: %w", err)
        }
    } else {
        // 服务渲染：只加载 <svc>.yaml，不合并 common
        tree, err = loadYAMLNode(filepath.Join(cfg.confDir, cfg.svc+".yaml"))
        if err != nil {
            return fmt.Errorf("加载 %s.yaml: %w", cfg.svc, err)
        }
    }

    values, err := loadYAMLNode(filepath.Join(cfg.valuesDir, cfg.env+".yaml"))
    if err != nil {
        return fmt.Errorf("加载 values/%s.yaml: %w", cfg.env, err)
    }

    fillLog := map[string]string{}
    upperPaths := map[string]bool{}
    resolving := map[string]bool{}
    if err := validateAndFill(tree, values, cfg.env, "", fillLog, upperPaths, resolving); err != nil {
        return err
    }

    if err := checkResidual(tree); err != nil {
        return err
    }

    if cfg.envFields != nil {
        if err := checkEnvCrossConsistency(tree, cfg.envFields, ""); err != nil {
            return err
        }
    }

    attachComments(tree, "", map[string]string{}, fillLog, upperPaths, cfg.reloadableFields)

    if err := os.MkdirAll(cfg.outDir, 0o755); err != nil {
        return fmt.Errorf("创建输出目录 %s: %w", cfg.outDir, err)
    }
    out := filepath.Join(cfg.outDir, "config.yaml")
    f, err := os.Create(out)
    if err != nil {
        return fmt.Errorf("创建 %s: %w", out, err)
    }
    defer f.Close()
    enc := yaml.NewEncoder(f)
    enc.SetIndent(2)
    if err := enc.Encode(tree); err != nil {
        return fmt.Errorf("写 %s: %w", out, err)
    }
    return enc.Close()
}
```

`deepMergeWithLog` 函数仍保留（测试中可能用到），但 `build` 不再调用它。

- [ ] **Step 6: 运行 config_build 测试**

```bash
go test ./tools/config_build/... -v
```

预期：全部 PASS。

- [ ] **Step 7: Commit**

```bash
git add tools/config_build/
git commit -m "refactor: config_build 移除深合并，common 独立渲染"
```

---

### Task 6: 更新 loader.go 和 loader_test.go

**Files:**
- Modify: `src/common/config/loader.go`
- Modify: `src/common/config/loader_test.go`

- [ ] **Step 1: 先写失败测试——common 独立加载 + 服务独立加载**

用新的分离 YAML 结构重写 `loader_test.go`：

```go
package config_test

import (
    "os"
    "path/filepath"
    "sync"
    "testing"

    conf "project/conf/schema/gen"
    cfgpkg "project/src/common/config"
)

const commonYAML = `
redis:
  host: "127.0.0.1"
  port: 6379
  timeout: 2
  password: "secret"
etcd:
  endpoints:
    - "localhost:2379"
nats:
  urls:
    - "nats://localhost:4222"
mongo:
  uri: "mongodb://localhost:27017"
  database: game
log:
  level: info
  dir: ./logs
version: "1.0.0"
region: default
excel_path: ./data/excel
`

const gateSvrYAML = `
meta:
  pre_application_list: []
  pre_application_path: ./bin
  pre_mine_application_list: []
node_id: "1.1.1"
server_type_name: gatesvr
addr: "0.0.0.0:8888"
heartbeat_sec: 30
shutdown_timeout_sec: 10
`

func writeYAML(t *testing.T, dir, content string) string {
    t.Helper()
    if err := os.MkdirAll(dir, 0755); err != nil {
        t.Fatal(err)
    }
    path := filepath.Join(dir, "config.yaml")
    if err := os.WriteFile(path, []byte(content), 0644); err != nil {
        t.Fatal(err)
    }
    return path
}

func TestLoadCommon_Basic(t *testing.T) {
    path := writeYAML(t, t.TempDir(), commonYAML)
    commonCfg, err := cfgpkg.LoadCommon(path)
    if err != nil {
        t.Fatalf("LoadCommon failed: %v", err)
    }
    if commonCfg.Redis.Host != "127.0.0.1" {
        t.Errorf("redis.host mismatch: %q", commonCfg.Redis.Host)
    }
}

func TestLoad_GateSvr_Basic(t *testing.T) {
    path := writeYAML(t, t.TempDir(), gateSvrYAML)
    loader := cfgpkg.NewLoader[conf.GateSvr](path)
    if err := loader.Load(); err != nil {
        t.Fatalf("Load GateSvr failed: %v", err)
    }
    cfg := loader.Current()
    if cfg.NodeId != "1.1.1" {
        t.Errorf("node_id mismatch: %q", cfg.NodeId)
    }
}

func TestLoad_StrictModeRejectsUnknownKey(t *testing.T) {
    yaml := gateSvrYAML + "\nunknown_key: bad_value\n"
    path := writeYAML(t, t.TempDir(), yaml)
    loader := cfgpkg.NewLoader[conf.GateSvr](path)
    if err := loader.Load(); err == nil {
        t.Fatal("expected strict-mode error for unknown field")
    }
}

func TestLoad_RequiredFieldMissing(t *testing.T) {
    yaml := `
meta:
  pre_application_path: ./bin
server_type_name: gatesvr
addr: "0.0.0.0:8888"
`
    path := writeYAML(t, t.TempDir(), yaml)
    loader := cfgpkg.NewLoader[conf.GateSvr](path)
    if err := loader.Load(); err == nil {
        t.Fatal("expected required field error for missing node_id")
    }
}

func TestLoad_EnvInjectionMissing(t *testing.T) {
    os.Unsetenv("MONGO_URI_TEST")
    yaml := `
redis:
  host: "127.0.0.1"
  port: 6379
  password: "secret"
etcd:
  endpoints: ["localhost:2379"]
nats:
  urls: ["nats://localhost:4222"]
mongo:
  uri: "${MONGO_URI_TEST}"
  database: game
`
    path := writeYAML(t, t.TempDir(), yaml)
    commonCfg, err := cfgpkg.LoadCommon(path)
    _ = commonCfg
    if err == nil {
        t.Fatal("expected env injection error for missing MONGO_URI_TEST")
    }
}

func TestLoad_EnvInjection_FillsValue(t *testing.T) {
    os.Setenv("MONGO_URI_TEST", "mongodb://testhost:27017")
    t.Cleanup(func() { os.Unsetenv("MONGO_URI_TEST") })
    yaml := `
redis:
  host: "127.0.0.1"
  port: 6379
  password: "secret"
etcd:
  endpoints: ["localhost:2379"]
nats:
  urls: ["nats://localhost:4222"]
mongo:
  uri: "${MONGO_URI_TEST}"
  database: game
`
    path := writeYAML(t, t.TempDir(), yaml)
    commonCfg, err := cfgpkg.LoadCommon(path)
    if err != nil {
        t.Fatalf("LoadCommon failed: %v", err)
    }
    if commonCfg.Mongo.Uri != "mongodb://testhost:27017" {
        t.Errorf("mongo.uri not injected: %q", commonCfg.Mongo.Uri)
    }
}

func TestReload_StaticFieldRejected(t *testing.T) {
    dir := t.TempDir()
    path := writeYAML(t, dir, gateSvrYAML)
    loader := cfgpkg.NewLoader[conf.GateSvr](path)
    if err := loader.Load(); err != nil {
        t.Fatal(err)
    }

    newYAML := `
meta:
  pre_application_list: []
  pre_application_path: ./bin
  pre_mine_application_list: []
node_id: "9.9.9"
server_type_name: gatesvr
addr: "0.0.0.0:9999"
heartbeat_sec: 30
shutdown_timeout_sec: 10
`
    os.WriteFile(path, []byte(newYAML), 0644)
    if err := loader.Reload(); err == nil {
        t.Fatal("expected reload rejection for static field change (node_id)")
    }
    if loader.Current().NodeId != "1.1.1" {
        t.Error("snapshot should not change after rejected reload")
    }
}

func TestReload_ReloadableFieldAccepted(t *testing.T) {
    dir := t.TempDir()
    path := writeYAML(t, dir, gateSvrYAML)
    loader := cfgpkg.NewLoader[conf.GateSvr](path)
    if err := loader.Load(); err != nil {
        t.Fatal(err)
    }

    newYAML := `
meta:
  pre_application_list: []
  pre_application_path: ./bin
  pre_mine_application_list: []
node_id: "1.1.1"
server_type_name: gatesvr
addr: "0.0.0.0:8888"
heartbeat_sec: 60
shutdown_timeout_sec: 10
`
    os.WriteFile(path, []byte(newYAML), 0644)
    if err := loader.Reload(); err != nil {
        t.Fatalf("reload of reloadable field should succeed: %v", err)
    }
    if loader.Current().HeartbeatSec != 60 {
        t.Errorf("heartbeat_sec should be 60, got %d", loader.Current().HeartbeatSec)
    }
}

func TestConcurrentReads(t *testing.T) {
    path := writeYAML(t, t.TempDir(), gateSvrYAML)
    loader := cfgpkg.NewLoader[conf.GateSvr](path)
    _ = loader.Load()
    var wg sync.WaitGroup
    for i := 0; i < 50; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 100; j++ {
                _ = loader.Current()
            }
        }()
    }
    wg.Wait()
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./src/common/config/... -v
```

预期：编译失败（`cfgpkg.LoadCommon` 尚未定义）。

- [ ] **Step 3: 更新 `loader.go`——修改 import 包名，新增 `LoadCommon`**

将 `loader.go` 中的 import 改为：

```go
import (
    "bytes"
    "fmt"
    "os"
    "os/signal"
    "reflect"
    "regexp"
    "strings"
    "sync/atomic"
    "syscall"
    "time"
    "unicode"

    "gopkg.in/yaml.v3"

    conf "project/conf/schema/gen"
)
```

在 `Loader` struct 定义之前新增 `LoadCommon` 独立函数：

```go
// LoadCommon 加载 run/common/conf/config.yaml 到 CommonConfig。
// 框架层统一调用，服务无需感知路径。
func LoadCommon(path string) (*conf.CommonConfig, error) {
    l := &Loader[conf.CommonConfig]{path: path}
    return l.loadAndValidate()
}
```

将 `validateRequired` 中的 `genconfig` 引用全部改为 `conf`：

```go
func validateRequired[T any](cfg *T, msgName string) error {
    required, ok := conf.RequiredFields[msgName]
    ...
}
```

将 `checkStaticFieldsUnchanged` 中的 `genconfig` 引用改为 `conf`：

```go
func checkStaticFieldsUnchanged[T any](old, newCfg *T) error {
    msgName := reflect.TypeOf(*old).Name()
    reloadable := conf.ReloadableFields[msgName]
    ...
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./src/common/config/... -v
```

预期：全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add src/common/config/
git commit -m "refactor: loader 新增 LoadCommon，包名引用改为 conf"
```

---

### Task 7: 更新各服务 main.go

**Files:**
- Modify: `src/servers/gatesvr/main.go`
- Modify: `src/servers/lobbysvr/main.go`
- Modify: `src/servers/onlinesvr/main.go`
- Modify: `src/servers/routersvr/main.go`

- [ ] **Step 1: 更新 `src/servers/gatesvr/main.go`**

将 import 中：
```go
genconfig "project/conf/schema/gen"
```
改为：
```go
conf "project/conf/schema/gen"
```

将 loader 初始化改为双加载：

```go
func main() {
    // 1. 加载公共配置
    commonCfg, err := cfgloader.LoadCommon("run/common/conf/config.yaml")
    if err != nil {
        panic("load common config: " + err.Error())
    }

    // 2. 加载 gatesvr 私有配置
    svrLoader := cfgloader.NewLoader[conf.GateSvr]("run/gatesvr/conf/config.yaml")
    svrLoader.MustLoad()
    cfg := svrLoader.Current()

    // 3. 日志
    log, _ := logger.NewZapDevelopment()
    logger.SetGlobal(log)

    // 4. 构造 NATS 集群
    self, err := cluster.ParseNodeID(cfg.NodeId)
    if err != nil {
        panic(err)
    }
    cls, err := transport.NewNatsCluster(self, transport.NatsClusterConfig{
        EtcdEndpoints:  commonCfg.Etcd.Endpoints,
        NatsURLs:       commonCfg.Nats.Urls,
        SelfAddr:       cfg.Addr,
        ServerTypeName: cfg.ServerTypeName,
    })
    if err != nil {
        panic(err)
    }

    // 5. Application
    app := application.NewBuilder().
        NodeID(cfg.NodeId).
        NodeType(cfg.ServerTypeName).
        Frontend(cfg.Addr).
        Serializer("protobuf", protobuf.NewSerializer()).
        Routes(routes.Config()).
        Cluster(cls).
        Build()

    gateModule := internal.NewGateModule(cfg.NodeId, app.Sessions(), app.Cluster(), app.AgentMap())
    app.Register(gateModule)
    if err := app.RegisterHandler(internal.NewGateHandler(gateModule), nil); err != nil {
        panic(err)
    }

    // 6. 生命周期
    app.Start()
    if err := cls.Init(); err != nil {
        panic(err)
    }
    defer cls.Stop()

    stop := svrLoader.Watch(30 * time.Second)
    defer stop()

    logger.Info("gatesvr started",
        logger.String("nodeID", cfg.NodeId),
        logger.String("addr", cfg.Addr))
    app.Run()
}
```

- [ ] **Step 2: 更新 `src/servers/lobbysvr/main.go`**

先读取当前文件内容，然后按同样模式修改：import 改 `conf`，加载 `commonCfg` + `conf.LobbySvr`，字段访问从 `cfg.LobbyCfg.NodeId` 改为 `cfg.NodeId`，`commonCfg.Etcd/Nats` 替代 `cfg.Common.Etcd/Nats`。

- [ ] **Step 3: 更新 `src/servers/onlinesvr/main.go`**

同上，`conf.OnlineSvr`，字段访问模式一致。

- [ ] **Step 4: 更新 `src/servers/routersvr/main.go`**

同上，`conf.RouterSvr`。

- [ ] **Step 5: 编译验证**

```bash
go build ./src/servers/...
```

预期：无编译错误。

- [ ] **Step 6: 运行全量测试**

```bash
go test ./...
```

预期：全部 PASS（matchsvr/roomsvr 使用旧 proto 尚未迁移，若有编译错误需检查其 import）。

- [ ] **Step 7: Commit**

```bash
git add src/servers/gatesvr/main.go src/servers/lobbysvr/main.go src/servers/onlinesvr/main.go src/servers/routersvr/main.go
git commit -m "refactor: 各服务 main.go 改用双 loader 加载（common + 私有配置分离）"
```

---

### Task 8: 更新文档

**Files:**
- Modify: `development.md`
- Modify: `architecture.md`

- [ ] **Step 1: 更新 `development.md` 中的命令示例**

将所有 `config.pb` 替换为 `config.pb.descriptor`，`config_*.proto` 替换为对应新文件名，相关命令示例对齐新 proto 文件列表：

```bash
protoc --proto_path=. \
       --descriptor_set_out=conf/schema/gen/config.pb.descriptor \
       --include_imports \
       conf/schema/options.proto \
       conf/schema/types.proto \
       conf/schema/common.proto \
       conf/schema/gatesvr.proto \
       conf/schema/lobbysvr.proto \
       conf/schema/onlinesvr.proto \
       conf/schema/routersvr.proto
go run ./tools/gen_config --pb=conf/schema/gen/config.pb.descriptor --out=conf/schema/gen
```

- [ ] **Step 2: 更新 `architecture.md` 中的相关引用**

将 `architecture.md` 中的 `config.pb` 改为 `config.pb.descriptor`。

- [ ] **Step 3: Commit**

```bash
git add development.md architecture.md
git commit -m "docs: 同步更新命令示例（descriptor 文件名、proto 文件名）"
```

---

### Task 9: 最终验证

- [ ] **Step 1: 全量编译**

```bash
go build ./...
```

预期：零错误。

- [ ] **Step 2: 全量测试**

```bash
go test ./...
```

预期：全部 PASS。

- [ ] **Step 3: 烘焙验证（需有 protoc 环境）**

```bash
go run ./tools/config_build --env=dev --svc=common
go run ./tools/config_build --env=dev --svc=gatesvr
```

检查产物：

```bash
# run/common/conf/config.yaml 应含 redis/etcd/nats/mongo，不含 node_id
# run/gatesvr/conf/config.yaml 应含 node_id/addr，不含 redis
```

- [ ] **Step 4: Commit 并推送**

```bash
git push -u origin docs/config-toolchain-redesign
```
