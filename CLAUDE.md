# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

本文件为 GameServer 仓库的总索引与最快入口。目标是让后续 Claude Code 实例用最少文件读取定位服务入口、配置、框架核心、协议和生成链路。

> 维护约定：凡是新增、删除、移动目录或包，修改启动流程、配置 schema、日志、命令入口、信号语义、热更语义、协议或生成链路时，都要同步更新本文件与对应子目录的 `CLAUDE.md`。

## 常用命令

- `make fmt`：执行 `go fmt ./...`。
- `make test`：执行 `go test ./...`。
- `go test ./internal/core/codec -run TestEncodeDecodeRequest`：运行单包单测；替换包路径和测试名即可。
- `go test ./internal/core/codec -bench BenchmarkEncodeRequest`：运行单包 benchmark。
- `make gen-config`：运行 `go run ./tools/configgen`，根据 `config/schema/*.proto` 生成 `config/gen/`。
- `make config ENV=dev WORLDID=1`：先生成配置代码，清空 `run/` 后再把 `config/values/dev.yaml` 烘焙进去；`WORLDID` 必填，范围 `1..65535`。
- `make build`：读取 `run/ENV` 和 `config/values/<env>.yaml` 的 `svr_list`，构建服务二进制并复制到 `run/<svc>/bin/`；需要先执行 `make config ...`。
- `make all ENV=dev WORLDID=1`：按 `config -> build -> test` 执行完整链路。
- `python scripts/config.py --env dev --world-id 1 --dry-run`：只打印配置烘焙计划，不写入 `run/`。
- `python scripts/build.py --out run --build build --svr gatesvr`：只构建一个服务；仍要求对应 runtime 配置已存在。
- `scripts/gen_proto.sh`：重新生成协议 `.pb.go`、`protocol/gen/handler`、`protocol/gen/remote`、`protocol/gen/rpc.go` 和 `protocol/gen/routes.go`。
- `go run ./tools/servergen --name <svc> --kind standard --register-env dev --dry-run`：预览新服务骨架生成；去掉 `--dry-run` 后才写文件。
- `make clean`：删除 `build/` 和 `run/`；`make run-clean` 只删除 `run/`。

运行目录由 `make config` 和 `make build` 生成；每次 `make config` 会先删除旧 `run/`。服务脚本在 `run/<svc>/bin/start.sh`、`run/<svc>/bin/stop.sh`，整体脚本在 `run/startall.sh`、`run/stopall.sh`；`run/startall.sh` 启动前会清空各服务 `log/`。命令行入口都支持 `--pid-file`、`--nodeid`、`--common-config`、服务私有 `--<svc>-config`、`--daemon`、`--pprof`、`--pprof-addr`。

## 项目概览

- Go module：`project`。
- Go 版本：`1.26`。
- 当前服务入口：`gatesvr`、`lobbysvr`、`onlinesvr`、`matchsvr`、`roomsvr`、`routeragent`。
- 当前开发环境服务列表来自 `config/values/dev.yaml` 的 `svr_list`，顺序也是 `run/startall.sh` 的启动顺序。
- `third_projects/` 是独立参考项目，不作为当前仓库实现事实源；除非任务直接指明要参考其中某个项目，否则不要自动读取该目录。

## 最快访问顺序

1. 先读本文件，确定目标目录。
2. 再读目标顶层目录的 `CLAUDE.md`。
3. 再读目标模块目录的 `CLAUDE.md`。
4. 最后只打开关键源码和测试。

不要默认全仓遍历。生成产物、运行产物、临时文件不要作为设计事实源；当文档与源码冲突时，以源码和测试为准，并同步修正文档。

## 顶层目录索引

| 目录 | 角色 | 最快入口 |
|---|---|---|
| `cmd/` | 服务入口，只负责 flags、Builder 装配和启动 | `cmd/CLAUDE.md` |
| `config/` | 配置 schema、模板、环境 values、生成产物 | `config/CLAUDE.md`, `docs/配置系统.md` |
| `internal/core/` | 框架核心：App 生命周期、配置、日志、进程、网络、会话、RPC、RouterAgent SDK/核心 | `internal/core/CLAUDE.md`, `docs/框架核心.md` |
| `internal/server/` | 具体服务实现：builder、config、options、module 和业务逻辑 | `internal/server/CLAUDE.md` |
| `pkg/` | 可复用公共库：logger、event、mongo、serialize、taskqueue、timewheel | `pkg/CLAUDE.md` |
| `protocol/` | 协议 proto 源文件与生成代码 | `protocol/CLAUDE.md`, `docs/协议与业务接口命名规则.md` |
| `tools/` | Go 开发工具与生成器 | `tools/CLAUDE.md`, `docs/代码生成工具.md` |
| `scripts/` | 构建、配置烘焙、协议生成脚本入口 | `scripts/CLAUDE.md` |
| `docs/` | 设计文档、命名规则、TODO 记录 | `docs/框架核心.md`, `docs/集群通信：路由代理.md` |
| `third_projects/` | 独立参考项目 | `third_projects/CLAUDE.md` |

## 架构大图

进程入口通常是：

```text
cmd/<svc>/main.go
  -> 解析 flags / daemon / pidfile
  -> internal/server/<svc>.New<Svc>Builder(opts)
  -> builder.Build()
  -> app.Startup()
```

`internal/core/app` 提供统一 App 和 Module 生命周期。服务 builder 负责加载 common config 和服务私有 config、初始化 logger group、创建 `BaseBuilder`、设置 daemon/pprof、注册 Module、shutdown hook 和 reload hook。跨 goroutine 修改共享状态时，应通过 `App.Post(fn)` 回到 App 主循环。

`gatesvr` 是客户端接入层，负责 TCP/WebSocket acceptor、codec、dispatcher、session，并通过本机 RouterAgent UDS 转发后端调用。`lobbysvr` 是后端节点，通过 RouterAgent UDS 接入集群，用生成的 handler route 注册适配器分发客户端入口请求；当前客户端入口保留 `LobbyHandler/Ping`。`onlinesvr`、`matchsvr`、`roomsvr` 是后端服务骨架和对应业务目录。`routeragent` 是集群通信 sidecar，业务进程通过本机 UDS 连接它，跨机器通信由 RouterAgent 之间的 TCP peer 完成。

RouterAgent 数据面核心不在 `internal/server/routeragent/`，而在 `internal/core/ragent/agent/`；业务服 SDK 在 `internal/core/ragent/sdk/`；wire 协议在 `internal/core/ragent/wire/`。修改 RouterAgent 服务外壳、配置和 CLI 时改 `internal/server/routeragent/`；修改路由、peer 转发、UDS/TCP 行为时改 `internal/core/ragent/`。

## 配置链路

配置事实源是 `config/schema/*.proto` 和 `config/*.yaml` / `config/*.yaml.tmpl`。`config/gen/` 是生成产物，不手改。`config/values/<env>.yaml` 提供环境值和 `svr_list`；`scripts/config.py` 会先重建 `run/`，再把模板烘焙到 `run/common/conf/` 和 `run/<svc>/conf/`，同时生成启动/停止脚本和 `run/ENV`。

运行时配置入口是 `config/gen` 生成的 `NewXxxConfigEntry`。服务包的 `config.go` 保存包级 entry，并提供 `CommonConfig()`、`GateConfig()`、`LobbyConfig()` 等访问函数。热更规则由 schema 的 `(config.reload)` 控制；未标记 reload 的字段变化会被 generated check 拒绝。

## 协议与生成链路

- `protocol/common/`：协议注解、错误码、节点类型。
- `protocol/handler/`：客户端入口 handler proto。
- `protocol/remote/`：服务间 RPC remote proto。
- `protocol/gen/`：生成的 route、handler、remote 和 typed RPC stub。
- `internal/core/ragent/proto/`：RouterAgent 内部 protobuf 协议。

改协议后运行 `scripts/gen_proto.sh`，再运行相关测试。`*.pb.go` 和 `protocol/gen/` 是生成产物，不手工改业务语义。生成链路涉及 `tools/gen_routes/` 与 `tools/protoc-gen-svcstub/`。

## 常见定位

- 查服务启动：先看 `cmd/<svc>/CLAUDE.md` 和 `cmd/<svc>/main.go`，再看 `internal/server/<svc>/CLAUDE.md`。
- 查框架生命周期、信号、reload、pprof：看 `docs/框架核心.md`、`internal/core/app/`、`internal/core/process/`、`internal/core/options/`。
- 查客户端接入：看 `docs/客户端接入（网关服）.md`、`internal/core/acceptor/`、`internal/core/conn/`、`internal/core/codec/`、`internal/core/dispatcher/`、`internal/core/session/`。
- 查 RouterAgent：看 `docs/集群通信：路由代理.md`、`internal/server/routeragent/`、`internal/core/ragent/`。
- 查 RPC：看 `docs/远程调用设计.md`、`internal/core/rpc/`、`protocol/remote/`、`protocol/gen/rpc.go`。
- 查配置：看 `docs/配置系统.md`、`config/CLAUDE.md`、`config/schema/`、`tools/configgen/`。
- 查代码生成：看 `docs/代码生成工具.md`、`tools/CLAUDE.md`、`scripts/CLAUDE.md`。
- 查节点 ID：看 `docs/寻址：节点编号.md`、`internal/core/nodeid/`。
- 查日志：看 `pkg/logger/`、`internal/core/logger/`。

## 修改规则

- 修改某个目录代码前，先读该目录最近的 `CLAUDE.md`，并读相邻同类包以匹配命名、错误处理、日志和注释风格。
- `cmd/` 不放业务逻辑；具体服务逻辑放 `internal/server/<svc>/`，框架能力放 `internal/core/`，可复用库放 `pkg/`。
- 改配置 schema、协议 proto 或生成器后，要重新生成并跑相关测试。
- 改启动流程、配置 schema、协议、生成链路、目录结构或工程约定时，同步更新根文档和相关子目录 `CLAUDE.md`。
- `third_projects/` 只作参考；除非任务明确指定要参考哪个项目，不要自动读取、修改或依据它推断当前实现。
