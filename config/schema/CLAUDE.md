# CLAUDE.md

本文件是 `config/schema/` 的局部索引。进入 schema 目录工作时，先读本文件，再回看生成代码和生成工具。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 配置 schema 的事实源。
- 这里的 proto 文件决定 `config/gen/` 的结构和校验逻辑。

## 主要文件

- [`common.proto`](common.proto)
- [`gate.proto`](gate.proto)
- [`lobby.proto`](lobby.proto)
- [`options.proto`](options.proto)
- [`types.proto`](types.proto)

## 快速读法

- 先看 `options.proto` 理解自定义 option。
- 再看 `types.proto`、`common.proto`，最后看服务私有 proto。
- 改完 schema 后，要回看 `config/gen/` 与生成工具。

## 工作规则

- `schema/` 优先于 `gen/`。
- 字段变更要同步生成代码和文档。
- 这里的 proto 文件是启动配置链路的主事实源。
