# CLAUDE.md

本文件是 `tools/servergen/` 的局部索引。进入服务壳生成器目录时，先读本文件，再看具体实现文件。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- 服务壳生成器。
- 这里负责生成 `cmd/<svc>/`、`internal/server/<svc>/`、`config/schema/<svc>.proto`、`config/<svc>.yaml` 等壳文件。

## 主要文件

- [`main.go`](main.go)
- [`generator.go`](generator.go)
- [`templates.go`](templates.go)

## 快速读法

- 先看 `main.go` 理解参数和入口。
- 再看 `generator.go` 理解计划、冲突检测和写入。
- 最后看 `templates.go` 理解模板内容。

## 工作规则

- 这里只生成服务壳，不生成业务逻辑。
- 默认不覆盖已有服务；只有显式 `--force` 才允许覆盖生成器负责的文件。
- 改模板时要同步生成方案文档。
