# CLAUDE.md

本文件是 `cmd/gatesvr/` 的局部索引。进入网关入口目录时，先读本文件，再看 `main.go` 和对应服务实现。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 网关服务的命令入口。
- 这里只做 flags 解析、Builder 组装、应用启动。

## 主要文件

- [`main.go`](main.go)

## 快速读法

- 先看 `main.go` 里的 flags、pid-file、daemon、pprof、config 路径。
- 再看 `internal/server/gate/CLAUDE.md`。
- 真正的应用行为在 `internal/server/gate/` 和 `internal/core/`。

## 工作规则

- 这里不要放业务逻辑。
- 修改启动参数时，要同步根文档和对应 builder 文档。
