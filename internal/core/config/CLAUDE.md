# CLAUDE.md

本文件是 `internal/core/config/` 的局部索引。进入配置核心目录工作时，先读本文件，再看 loader / entry。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 配置加载、热更入口、配置 entry 与运行时加载流程。

## 主要文件

- [`entry.go`](entry.go)
- [`loader.go`](loader.go)

## 快速读法

- 先看 `loader.go` 理解 YAML 读取与环境变量展开。
- 再看 `entry.go` 理解配置承载与访问方式。

## 工作规则

- 配置加载行为变更时，要同步 `config/` 顶层索引与 `config/gen/`。
- 运行时热更和启动加载要区分清楚。
