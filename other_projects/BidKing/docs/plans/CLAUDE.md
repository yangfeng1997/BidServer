# CLAUDE.md

本文件是 `other_projects/BidKing/docs/plans/` 的局部索引。进入本目录工作时，先读本文件，再按需读取相邻源码、测试或上级文档。

> **维护约定**：本文件只记录本层目录边界与导航；当本目录新增、删除、移动关键文件或子目录时，同步更新索引。不要在这里复制上级 `CLAUDE.md` 的完整内容。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [BidKing/CLAUDE.md](../../CLAUDE.md)

## 目录定位

- 本目录索引用于快速定位文件与子目录，具体行为以源码、测试和上级文档为准。

## 主要文件

- [`2026-06-01-P1-gateway-lobby-login.md`](2026-06-01-P1-gateway-lobby-login.md)
- [`2026-06-02-P2-router-online-impl-plan.md`](2026-06-02-P2-router-online-impl-plan.md)
- [`2026-06-02-P2-router-online.md`](2026-06-02-P2-router-online.md)
- [`2026-06-02-implementation-roadmap-P2-P5.md`](2026-06-02-implementation-roadmap-P2-P5.md)
- [`2026-06-03-P3a-lobby-ec-mongodb-impl-plan.md`](2026-06-03-P3a-lobby-ec-mongodb-impl-plan.md)
- [`2026-06-03-P3a-lobby-ec-mongodb.md`](2026-06-03-P3a-lobby-ec-mongodb.md)
- [`2026-06-03-P3b-friend-mail-currency-touch-presence-impl-plan.md`](2026-06-03-P3b-friend-mail-currency-touch-presence-impl-plan.md)
- [`2026-06-03-P3b-friend-mail-currency-touch-presence.md`](2026-06-03-P3b-friend-mail-currency-touch-presence.md)
- [`2026-06-04-P4-match-room-auction.md`](2026-06-04-P4-match-room-auction.md)
- [`2026-06-04-P4a-matchmaking-open-game-impl-plan.md`](2026-06-04-P4a-matchmaking-open-game-impl-plan.md)
- [`2026-06-04-P4a-matchmaking-open-game.md`](2026-06-04-P4a-matchmaking-open-game.md)
- [`2026-06-04-P4b-auction-settlement-impl-plan.md`](2026-06-04-P4b-auction-settlement-impl-plan.md)
- [`2026-06-04-P4b-auction-settlement.md`](2026-06-04-P4b-auction-settlement.md)
- [`2026-06-04-pre-P4-proto-convergence-impl-plan.md`](2026-06-04-pre-P4-proto-convergence-impl-plan.md)
- [`2026-06-05-P4c-reconnect-rejoin-impl-plan.md`](2026-06-05-P4c-reconnect-rejoin-impl-plan.md)
- [`2026-06-05-P4c-reconnect-rejoin.md`](2026-06-05-P4c-reconnect-rejoin.md)
- [`2026-06-05-P5-A-cleanup-impl-plan.md`](2026-06-05-P5-A-cleanup-impl-plan.md)
- [`2026-06-05-P5-A-cleanup.md`](2026-06-05-P5-A-cleanup.md)
- [`2026-06-05-P5-flush-atomicity-impl-plan.md`](2026-06-05-P5-flush-atomicity-impl-plan.md)
- [`2026-06-05-P5-flush-atomicity.md`](2026-06-05-P5-flush-atomicity.md)
- [`2026-06-06-P5-B-framework-robustness-impl-plan.md`](2026-06-06-P5-B-framework-robustness-impl-plan.md)
- [`2026-06-06-P5-B-framework-robustness.md`](2026-06-06-P5-B-framework-robustness.md)
- [`2026-06-06-P5-B1B7-request-lifecycle-impl-plan.md`](2026-06-06-P5-B1B7-request-lifecycle-impl-plan.md)
- [`2026-06-06-P5-B1B7-request-lifecycle.md`](2026-06-06-P5-B1B7-request-lifecycle.md)
- [`2026-06-06-P5-B3-taskqueue-backpressure-impl-plan.md`](2026-06-06-P5-B3-taskqueue-backpressure-impl-plan.md)
- [`2026-06-06-P5-B3-taskqueue-backpressure.md`](2026-06-06-P5-B3-taskqueue-backpressure.md)
- [`2026-06-06-P5-E-opdedup-eviction-impl-plan.md`](2026-06-06-P5-E-opdedup-eviction-impl-plan.md)
- [`2026-06-06-P5-E-opdedup-eviction.md`](2026-06-06-P5-E-opdedup-eviction.md)
- [`2026-06-08-cli-daemon-framework.md`](2026-06-08-cli-daemon-framework.md)
- [`2026-06-08-config-toolchain-plan-a-go.md`](2026-06-08-config-toolchain-plan-a-go.md)
- [`2026-06-08-config-toolchain-plan-b-python.md`](2026-06-08-config-toolchain-plan-b-python.md)

## 工作规则

- 不要把本目录规则外推到其他参考项目或兄弟目录。
- 修改代码前先读同目录测试和相邻实现，保持命名、错误处理、日志和注释风格一致。
- 生成产物、二进制、临时文件不要作为设计事实源；如必须改生成产物，先找到对应源文件或生成命令。
- 如果本索引与源码冲突，以源码和测试为准，并同步修正文档。
