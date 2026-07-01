# CLAUDE.md

本文件为 BidServer 仓库的总索引与最快入口。目标是让你**最少读文件**就能定位到服务入口、配置、框架核心和生成链路。

> **维护约定**：凡是新增、删除、移动目录或包，修改启动流程、配置 schema、日志、命令入口、信号语义、热更语义、生成链路时，都要同步更新本文件与对应子目录的 `CLAUDE.md`。

## 最快访问顺序

1. 先读本文件，确定目标目录。
2. 再读目标顶层目录的 `CLAUDE.md`。
3. 再读目标模块目录的 `CLAUDE.md`。
4. 最后只打开关键源码和测试。

## 文档同步规则

- 当我修改某个目录的代码，且改动会影响该目录的结构、入口、配置、生命周期、命令、信号、热更语义或生成链路时，我会同步更新该目录以及必要的上级 `CLAUDE.md`。
- 纯实现细节的小修补通常不需要改文档，但如果它会改变索引、加载顺序或工程约定，我会补写。
- 如果我发现文档和代码冲突，我会优先修正文档，让索引始终反映当前仓库状态。

## 顶层目录索引

| 目录 | 角色 | 最快入口 |
|---|---|---|
| `cmd/` | 服务入口 | `cmd/CLAUDE.md` |
| `config/` | 配置源、schema、生成产物、values | `config/CLAUDE.md` |
| `internal/` | 项目私有实现 | `internal/CLAUDE.md` |
| `pkg/` | 可复用公共库 | `pkg/CLAUDE.md` |
| `tools/` | 构建、生成、烘焙工具 | `tools/CLAUDE.md` |
| `other_projects/` | 独立参考项目 | `other_projects/CLAUDE.md` |

## 当前项目概览

- 模块路径：`project`
- Go 版本：`1.26`
- 主服务：`gatesvr`、`lobbysvr`、`routeragent`
- 主要配置目录：`config/`
- 核心代码：`internal/core/`
- 服务实现：`internal/server/`

## 入口建议

- 查服务启动：先看 `cmd/<svc>/`，再看 `internal/server/<svc>/`。
- 查配置：先看 `config/CLAUDE.md`，再看 `config/schema/` 与 `config/gen/`。
- 查框架核心：先看 `internal/CLAUDE.md`，再看 `internal/core/`。
- 查节点 ID：先看 `internal/core/nodeid/`。
- 查日志：先看 `pkg/logger/` 与 `internal/core/logger/`。
- 查工具：先看 `tools/CLAUDE.md`，再看 `tools/configgen/` 与 `tools/config.py`、`tools/build.py`。

## 工程纪律

- 不要默认全仓遍历；只沿着索引往下读。
- 生成产物、运行产物、临时文件不要作为设计事实源。
- 发现索引与源码冲突时，以源码和测试为准，并同步修正文档。
- 修改前先读相邻同类包，保持命名、错误处理、日志和注释风格一致。

## 规则优先级

1. 任务明确要求优先。
2. 更具体的子目录 `CLAUDE.md` 优先于本文件。
3. 源码和测试优先于文档。
4. 本文件只管仓库总索引和最快加载路径。
5. 不明确时先问，不要默默猜。
