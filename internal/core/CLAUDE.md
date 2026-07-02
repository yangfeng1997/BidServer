# CLAUDE.md

本文件是 `internal/core/` 的局部索引。进入框架核心目录工作时，先读本文件，再进入具体子包。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- 框架核心实现目录。
- 这里放 App 生命周期、配置、日志、进程管理、节点 ID、网络、编解码、分发、会话、RPC 等基础能力。

## 子目录

- [`acceptor/`](acceptor/)
- [`app/`](app/)
- [`codec/`](codec/)
- [`config/`](config/)
- [`conn/`](conn/)
- [`dispatcher/`](dispatcher/)
- [`errcode/`](errcode/)
- [`logger/`](logger/)
- [`nodeid/`](nodeid/)
- [`options/`](options/)
- [`process/`](process/)
- [`rpc/`](rpc/)
- [`session/`](session/)

## 快速读法

- 查生命周期先看 `app/`。
- 查网络接入先看 `acceptor/`、`conn/`、`codec/`。
- 查消息分发先看 `dispatcher/`、`session/`。
- 查 RPC 先看 `rpc/`。
- 查配置加载和热更先看 `config/`。
- 查日志先看 `logger/`。
- 查节点 ID 编解码先看 `nodeid/`。
- 查命令行 / daemon / pidfile / signal 先看 `options/` 和 `process/`。
