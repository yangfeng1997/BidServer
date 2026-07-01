# CLAUDE.md

本文件是 `other_projects/` 的总入口索引，目标是让你**最快**定位到某个参考项目的根文档、热点模块和关键源码。

> **维护约定**：这里只记录项目边界、最快路径和加载顺序；具体项目的详细规则写在各自根目录 `CLAUDE.md`，更细的子包索引写在对应子目录 `CLAUDE.md`。不要把所有细节堆在总索引里。

## 最快访问顺序

1. 先读本文件，确定项目名。
2. 再读目标项目根目录 `CLAUDE.md`。
3. 只读该项目的热点模块索引或热点源码文件。
4. 只有当热点索引不够时，才继续深入子目录 `CLAUDE.md`。

## 项目总览

| 目录 | 类型 | 最快入口 |
|---|---|---|
| `BidKing/` | 多服务游戏服务器框架 / 业务骨架 | 先读 `BidKing/CLAUDE.md`，热点优先看 `src/framework/`、`src/common/config/`、`src/common/logger/`、`src/servers/<svc>/` |
| `GameServer/` | Go 游戏服务器框架骨架 | 先读 `GameServer/CLAUDE.md`，热点优先看 `internal/core/app/`、`internal/core/config/`、`internal/core/log/`、`internal/core/ragent/`、`cmd/<svc>/` |
| `ProjectBid/` | Pitaya 风格游戏服务器框架实验项目 | 先读 `ProjectBid/CLAUDE.md`，热点优先看 `application/`、`service/`、`session/`、`cluster/`、`discovery/`、`config/` |
| `cherry/` | Actor Model 游戏服务器框架参考 | 先读 `cherry/CLAUDE.md`，热点优先看 `application.go`、`cherry.go`、`facade/`、`net/actor/`、`net/cluster/`、`net/discovery/`、`net/parser/` |
| `pitaya/` | Pitaya v2 参考项目 | 先读 `pitaya/CLAUDE.md`，热点优先看 `builder.go`、`app.go`、`service/`、`session/`、`cluster/`、`config/`、`acceptor/`、`agent/` |

## 使用方式

- 查单个项目：只进入对应目录，先读该项目根 `CLAUDE.md`。
- 查某个模块：优先读该项目的热点模块索引，再直接打开热点源码文件。
- 横向对比：按项目逐个读，结论要落到各自真实符号和文件上。
- 不要默认全仓遍历；只有用户明确要求才做多项目比较。

## 工作纪律

- 这些参考项目彼此独立，不能假设包名、配置、生命周期或协议语义一致。
- 不要跨项目批量修改，除非用户明确要求。
- 不要把生成产物、二进制、临时文件当作事实源。
- 如果子项目文档与源码冲突，以源码和测试为准，并同步修正文档。

## 规则优先级

1. 用户当前任务的明确要求优先。
2. 子项目自己的 `CLAUDE.md` 优先于本文件。
3. 子项目源码和测试优先于文档。
4. 本文件只管 `other_projects/` 的总边界和最快加载顺序。
5. 不明确时先问，不要默默猜。
