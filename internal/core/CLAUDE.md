# CLAUDE.md

本文件是 `internal/core/` 的局部索引。进入框架核心目录工作时，先读本文件，再进入具体子包。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- 框架核心实现目录。
- 这里放 App 生命周期、配置、日志、进程管理、节点 ID 等基础能力。

## 子目录

- [`app/`](app/)
- [`config/`](config/)
- [`logger/`](logger/)
- [`nodeid/`](nodeid/)
- [`options/`](options/)
- [`process/`](process/)

## 快速读法

- 查生命周期先看 `app/`。
- 查配置加载和热更先看 `config/`。
- 查日志先看 `logger/`。
- 查节点 ID 编解码先看 `nodeid/`。
- 查命令行 / daemon / pidfile / signal 先看 `options/` 和 `process/`。
