# CLAUDE.md

本文件是 `tools/` 的局部索引。进入开发工具目录工作时，先读本文件，再进入具体工具包。

> **维护约定**：本文件只记录生成与构建工具的导航；当新增、删除、移动工具脚本或工具包时同步更新索引。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 开发期 Go 工具目录。
- 包括配置生成器、协议路由生成器、stub 生成器和服务壳生成器。

## 子目录

- [`configgen/`](configgen/)
- [`gen_routes/`](gen_routes/)
- [`protoc-gen-svcstub/`](protoc-gen-svcstub/)
- [`servergen/`](servergen/)

## 快速读法

- `configgen/` 负责根据配置 schema 生成 `config/gen/`。
- `gen_routes/` 负责根据 handler proto 生成路由表。
- `protoc-gen-svcstub/` 负责生成 handler / remote / RPC stub。

## 工作规则

- 脚本型编排入口放在 `scripts/`，Go 工具源码放在 `tools/`。
- 改生成链路时先看输入 / 输出，再看调用顺序。
- `__pycache__/` 这类缓存不作为文档目标。
