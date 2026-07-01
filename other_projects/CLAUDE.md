# CLAUDE.md

本文件为 Claude Code 在 `other_projects/` 目录下工作时的总索引与边界说明。这里存放的是多个**独立参考项目**，不是主仓库 BidServer 的源码组成部分。

> **维护约定**：当新增、删除、重命名参考项目目录，或某个参考项目的定位发生变化时，同步更新本文件。具体项目的架构、命令、代码约定应写在对应子目录自己的 `CLAUDE.md` 中，不要把所有细节塞进本文件。

---

## 目录定位

`other_projects/` 用于存放独立参考项目，主要作为 BidServer 架构设计、实现取舍、命名和工程实践的参考来源。

默认规则：

- 不要把 `other_projects/` 当作一个统一 Go module 或统一仓库处理。
- 不要默认全量 grep / 遍历所有参考项目；只在任务明确要求某个项目或某类参考时进入相关目录。
- 进入具体项目后，优先读取该项目自己的 `CLAUDE.md`。
- 修改某个参考项目时，只遵循该子项目的 `CLAUDE.md` 和该项目源码事实，不把其他项目规则混用。
- 回答 BidServer 主仓库问题时，只有用户明确要求参考这些项目，才引用这里的内容。

---

## 子项目索引

| 目录 | 类型 | 说明 | 进入后先读 |
|---|---|---|---|
| `BidKing/` | Go 游戏服务器框架 / 业务骨架 | 多服务游戏服务器框架，包含 gate、lobby、online、router、room、match 等服务，具备配置生成、RPC、路由、匹配、房间等完整骨架 | `BidKing/CLAUDE.md` |
| `GameServer/` | Go 游戏服务器框架骨架 | 基于设计文档落地的多服务框架，含 `cmd/` 服务入口、`internal/core/` 框架层、`protocol/`、`conf/` 和生成工具 | `GameServer/CLAUDE.md` |
| `ProjectBid/` | Pitaya 风格 Go 框架实验项目 | 轻量游戏服务器框架，含 Application/Builder、Component、HandlerService、NATS RPC、etcd discovery、TCP/WS/KCP acceptor | `ProjectBid/CLAUDE.md` |
| `cherry/` | 开源 Go 游戏服务器框架参考 | Cherry 框架，基于 Actor Model，包含应用装配、组件生命周期、Actor、集群、连接器、profile、日志等 | `cherry/CLAUDE.md` |
| `pitaya/` | 开源 Go 游戏服务器框架参考 | Pitaya v2，支持 handler/remote、session、cluster RPC、service discovery、metrics、tracing、pipeline、worker、group 等 | `pitaya/CLAUDE.md` |

---

## 使用方式

### 查一个具体参考项目

如果用户明确说“看 BidKing / cherry / pitaya / ProjectBid / GameServer”，只进入对应目录，先读对应 `CLAUDE.md`，再按需读源码和文档。

### 对比多个参考项目

如果用户要求横向对比，例如“比较 pitaya 和 cherry 的生命周期”，可以进入多个目录，但要分别读取各自的 `CLAUDE.md`，并在回答中明确每个结论来自哪个项目的哪个符号或文档。

### 为 BidServer 提供架构参考

参考这些项目时，应把结论落回 BidServer 的具体符号和约束上，不要直接照搬：

- 生命周期参考时，对照 BidServer 的 `internal/core/app`。
- 配置参考时，对照 BidServer 的 `config/schema/*.proto`、`config/gen/`、`tools/config*.py`。
- 网络 / RPC / 路由参考时，对照 BidServer 现有服务边界和当前实现状态。
- 日志参考时，对照 BidServer 的 `pkg/logger` 与 `internal/core/logger`。

---

## 工作纪律

- 这些参考项目之间互相独立，不能假设包名、配置、生命周期或协议语义一致。
- 不要跨项目批量修改，除非用户明确要求。
- 不要把某个参考项目的生成产物、二进制、临时文件当作主事实来源。
- 发现某个子项目的 `CLAUDE.md` 与源码冲突时，以源码为准，并同步修正文档。
- 生成或更新子项目文档时，优先写索引、命令、真实目录结构、生命周期和约束，避免泛泛描述。

---

## 上下文加载规则

- 只处理 `other_projects/` 根目录本身时，读取本文件即可。
- 处理某个子项目时，先读该子项目的 `CLAUDE.md`。
- 需要构建/测试时，再读该子项目的 `Makefile`、README、构建文档或脚本。
- 需要改代码时，再读相邻同类包，对齐其命名、错误处理、日志和测试风格。
- 需要横向比较时，按项目逐个加载，不要一次性全量读取所有源码。

---

## 规则优先级

规则冲突时：

1. 用户当前任务的明确要求优先。
2. 当前子项目自己的 `CLAUDE.md` 优先于本文件。
3. 子项目源码和测试优先于文档。
4. 本文件只管 `other_projects/` 的总边界和加载方式。
5. 不明确时先问，不要默默猜。
