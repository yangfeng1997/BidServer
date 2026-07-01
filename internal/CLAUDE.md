# CLAUDE.md

本文件是 `internal/` 的局部索引。进入内部实现目录工作时，先读本文件，再进入 core / server 子目录。

> **维护约定**：本文件只记录内部实现分层与导航；当新增、删除、移动内部包时同步更新索引。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 项目私有实现代码。
- `core/` 放框架核心，`server/` 放具体服务实现。

## 子目录

- [`core/`](core/)
- [`server/`](server/)

## 快速读法

- 改框架能力先看 `internal/core/`。
- 改具体服务先看 `internal/server/`。
- 这里的包不要当作可复用库对外引用。
