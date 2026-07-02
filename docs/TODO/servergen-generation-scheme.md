# servergen 服务壳生成方案

本文件记录当前对 `servergen` 的阶段性方案。它的目标不是生成业务逻辑，而是**一键生成可运行的服务壳**，让后续新增服务都先落入统一框架，再单独手写业务实现。

---

## 目标

- 后续持续新增服务时，不再手工搭建 `cmd/`、`internal/server/`、`config/schema/`、`config/` 这些重复骨架。
- `routeragent` 作为第一个生成出来的服务壳，用来验证模板是否足够通用。
- 生成完成后，再进入 routeragent 的业务设计与实现阶段。

---

## 生成器职责边界

### 负责生成

- `cmd/<svc>/main.go`
- `cmd/<svc>/CLAUDE.md`
- `internal/server/<svc>/builder.go`
- `internal/server/<svc>/options.go`
- `internal/server/<svc>/config.go`
- `internal/server/<svc>/CLAUDE.md`
- `config/schema/<svc>.proto`
- `config/<svc>.yaml`
- 必要时同步 `config/values/dev.yaml`、`config/values/prod.yaml` 的 `svr_list`

### 不负责生成

- 路由转发逻辑
- peer 管理
- 广播逻辑
- 节点发现
- 心跳保活
- RPC wire format
- 任何业务 handler

---

## 输入参数建议

建议 `servergen` 至少支持以下参数：

- `--name <svc>`：服务名，例如 `routeragent`
- `--kind standard|sidecar`：服务类型，默认 `standard`
- `--register-env dev,prod`：是否将服务写入环境列表
- `--dry-run`：只输出计划，不写文件
- `--force`：覆盖已有文件

示例：

```bash
go run ./tools/servergen --name routeragent --kind sidecar --register-env dev,prod
```

---

## 覆盖策略

- **默认不覆盖已有服务**：如果 `cmd/<svc>/`、`internal/server/<svc>/`、`config/schema/<svc>.proto` 等目标路径已经存在，直接报错退出。
- **`--force` 才允许覆盖**：仅覆盖生成器负责的壳文件，不默认碰业务逻辑文件。
- **`--dry-run` 只预览**：只打印将要创建/覆盖的文件清单，不写盘。
- **优先保护手写内容**：一旦服务已经进入手写阶段，生成器就不应再自动重写它的业务文件。

---

## 第一版模板内容

### `cmd/<svc>/main.go`

职责：

- 解析 flags
- 校验必要参数
- 处理 daemon / pidfile / pprof
- 创建 builder
- `Build()`
- `Startup()`

不放业务代码。

### `internal/server/<svc>/options.go`

职责：

- 定义服务启动参数
- 嵌入 `core/options.BaseOptions`
- 持有本服务自己的配置路径

### `internal/server/<svc>/config.go`

职责：

- 持有 common config 和 service config 的 entry
- 提供 `Set...ConfigEntry`
- 提供 `ReloadConfig()`
- 提供 `AddConfigChangeHook()`

### `internal/server/<svc>/builder.go`

职责：

- 先加载 common config
- 再加载 service config
- 初始化 logger group
- 组装 `app.BaseBuilder`
- 挂 shutdown hook
- 挂 reload hook

### `config/schema/<svc>.proto`

职责：

- 定义该服务的配置 schema
- 标记 root/server/reload/required/env 等语义

### `config/<svc>.yaml`

职责：

- 作为烘焙模板输入
- 进入 `scripts/config.py` / `scripts/config_bake.py` 现有流程

---

## 生成器目录结构

建议 `servergen` 先按下面的目录拆分，保证模板和逻辑分离：

```text
tools/servergen/
├── main.go
├── generator.go
├── plan.go
├── render.go
└── template/
    ├── cmd_main.tmpl
    ├── server_builder.tmpl
    ├── server_config.tmpl
    ├── server_options.tmpl
    ├── schema.proto.tmpl
    ├── claude_cmd.tmpl
    └── claude_server.tmpl
```

### 各文件职责

- `main.go`：解析参数、调用生成器、输出结果
- `plan.go`：检查目标是否存在、计算创建/覆盖计划
- `generator.go`：执行写入、处理 `--dry-run` / `--force`
- `render.go`：模板渲染与变量填充
- `template/`：所有壳文件模板

---

## 模板变量清单

第一版模板建议只支持少量稳定变量，避免模板过度复杂：

- `ServiceName`：服务名，如 `routeragent`
- `ServiceKind`：服务类型，如 `standard` / `sidecar`
- `CmdPackage`：入口包名，通常是 `main`
- `ServerPackage`：服务实现包名，通常与服务名一致
- `ProtoGoPackage`：schema 生成后的 Go package 路径
- `CommonConfigPath`：公共配置路径
- `ServiceConfigPath`：服务配置路径

如果后面要支持更多服务形态，再逐步补变量，不要一开始就做成通用模板引擎。

---

## 生成流程建议

1. 解析参数，得到 `name`、`kind`、`dry-run`、`force`、`register-env`
2. 计算目标路径
3. 检查目标是否存在
4. 生成写入计划
5. `--dry-run` 只打印计划
6. 非 `--dry-run` 时按计划写文件
7. 如启用环境注册，再更新 `config/values/*.yaml`
8. 最后输出本次生成结果

---

## routeragent 的定位

`routeragent` 不应被当作特殊业务服，而应被当作：

> **servergen v1 的首个生成目标**

也就是说：

1. 先生成壳
2. 再写 routeragent 内部逻辑
3. 用 routeragent 验证模板是否足够通用

---

## 与现有工具链的衔接

`servergen` 生成后的结果要能直接接上当前仓库约定：

- `scripts/config.py` 依据 `svr_list` 发现服务
- `scripts/build.py` 依据 `cmd/<svc>` 编译服务
- `config/schema/*.proto` 作为配置事实源
- `config/<svc>.yaml` 作为烘焙输入
- `config/values/*.yaml` 决定环境里有哪些服务

---

## v1 落地顺序

1. 先定模板边界
2. 再做 `tools/servergen`
3. 先支持 `routeragent`
4. 只保证壳可编译、可运行、可接配置
5. 业务逻辑留到生成完成后再设计

---

## 备注

这个方案的核心不是“少写几个文件”，而是：

- 让新服务天然进入统一结构
- 降低漏配、漏文档、漏入口的概率
- 让后续服务扩展成本稳定
