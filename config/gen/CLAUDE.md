# CLAUDE.md

本文件是 `config/gen/` 的局部索引。进入生成代码目录时，先读本文件，再回看 `schema/` 源文件和生成工具。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 配置 schema 的生成产物。
- 这里的代码由 `tools/configgen` 生成。

## 主要文件

- [`config.go`](config.go)
- [`entry.go`](entry.go)
- [`loader.go`](loader.go)
- [`reload.go`](reload.go)
- [`source_test.go`](source_test.go)
- [`validate.go`](validate.go)

## 快速读法

- 如果要理解某个结构体字段来自哪里，先回 `config/schema/*.proto`。
- 如果要理解 reload 或 validate 行为，先看 `reload.go` 和 `validate.go`。
- 生成文件不要手改，改动应回到 schema 或生成工具。

## 工作规则

- 生成产物不作为设计事实源。
- 修改生成逻辑时，要同步检查 `tools/configgen/`。
