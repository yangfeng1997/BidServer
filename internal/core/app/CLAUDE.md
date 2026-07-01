# CLAUDE.md

本文件是 `internal/core/app/` 的局部索引。进入应用生命周期目录工作时，先读本文件，再看具体实现文件。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- App 生命周期、模块管理、启动 / 停止、Post 主循环投递与 ready 协调。

## 主要文件

- [`app.go`](app.go)
- [`builder.go`](builder.go)
- [`module.go`](module.go)
- [`options.go`](options.go)
- [`ready.go`](ready.go)

## 关键接口

- `App.Post` / `Poster`：跨 goroutine 投递任务到 app 主循环，底层使用 `pkg/taskqueue` 的任务 channel。
- `Ready` / `ReadyWaiter`：模块异步初始化后的就绪等待。

## 快速读法

- 先看 `app.go` 的 `Start/Shutdown/Run/Fini`。
- 再看 `builder.go` 的构建与依赖校验。
- 再看 `module.go` 的模块接口。
- 查 ready 协调看 `ready.go`。
- 最后看 `options.go` 的命令行 / 启动参数。

## 工作规则

- 这里是生命周期中心，改动要特别注意 channel 关闭、WaitGroup、signal、幂等停服。
- 改启动流程时要同步顶层根文档。
