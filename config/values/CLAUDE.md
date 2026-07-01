# CLAUDE.md

本文件是 `config/values/` 的局部索引。进入 values 目录工作时，先读本文件，再看环境文件。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 环境值输入目录。
- 这里决定 config 烘焙时各环境的值与服务列表。

## 主要文件

- [`dev.yaml`](dev.yaml)
- [`prod.yaml`](prod.yaml)

## 快速读法

- 想知道当前环境有哪些服务，先看 `svr_list`。
- 改烘焙值先看对应环境文件。
- 服务列表变化时，要同步构建脚本和运行时配置约定。

## 工作规则

- 不要把 `values/` 和 `schema/` 混为一谈。
- `svr_list` 是构建与配置流程的关键入口。
