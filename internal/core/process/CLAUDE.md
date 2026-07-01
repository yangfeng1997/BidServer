# CLAUDE.md

本文件是 `internal/core/process/` 的局部索引。进入进程管理目录工作时，先读本文件，再看 daemon / pidfile / signal。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- daemon、pidfile、signal 与进程生命周期控制。

## 主要文件

- [`daemon.go`](daemon.go)
- [`pidfile.go`](pidfile.go)
- [`signal.go`](signal.go)

## 快速读法

- 先看 `signal.go` 明白监听哪些系统信号。
- 再看 `pidfile.go` 了解 PID 文件读写。
- 最后看 `daemon.go` 了解后台化逻辑。

## 工作规则

- 信号语义改动会影响服务停服行为，必须同步根文档。
- pid-file 与 daemon 的重复 close / 重入语义要保持幂等。
