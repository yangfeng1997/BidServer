# CLAUDE.md

本文件是 `tools/configgen/` 的局部索引。进入配置生成器目录时，先读本文件，再看 main.go。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 配置结构生成器。
- 这里负责从 schema 生成 Go 代码、entry、loader、reload 校验等产物。

## 主要文件

- [`main.go`](main.go)

## 快速读法

- 先看 `main.go` 里的输入输出参数和生成流程。
- 再回看 `config/schema/` 与 `config/gen/`。

## 工作规则

- 生成工具改动后，要同步 `config/` 顶层索引和生成产物说明。
- 生成产物不要手改，改源头或生成器。
