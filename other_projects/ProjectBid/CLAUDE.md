# CLAUDE.md

此文件为 Claude Code (claude.ai/code) 在本仓库中工作时提供指导。

## 项目概览

现代化、高性能分布式微服务 Go 游戏服务器。全新项目 — 暂无已有代码。

### 技术栈

- **语言:** Go（最新稳定版）
- **协议:** TCP、WebSocket、KCP（客户端-网关通信）
- **序列化:** Protobuf
- **服务间通信:** NATS（nats.go）
- **服务发现 / 配置:** etcd
- **缓存 / 状态:** go-redis
- **持久化存储:** mongo-driver（MongoDB）
- **日志:** zap
- **ID 生成:** bwmarrin/snowflake（Snowflake 分布式 ID）
- **UUID:** google/uuid 或类似方案
- **调度:** cron + 时间轮（游戏内定时器）
- **架构:** 微服务架构，包含网关、集群管理及各服务模块
- 等其他的参考其他开源代码

## 关键工作流程

在实现任何功能之前，必须遵循以下流程：

1. **阅读参考实现** — 在全部 5 个参考框架中（路径见下方）找到与当前任务相关的代码。常见入口文件：`app.go`、`application.go`、`server.go`、`agent.go`、`module.go`。
2. **分析每个框架的方案** — 识别设计上的优点和缺点。
3. **综合最佳设计** — 吸收各框架的优点，规避缺陷。
4. **实现** — 仅在第 3 步完成后方可在此项目中编写代码。

### 参考框架路径

| # | 框架 | 路径 | 风格 |
|---|-----------|------|-------|
| 1 | Pitaya 2.11.22 | `D:\goserver\pitaya-2.11.22\pitaya-2.11.22` | Actor 模型，可扩展性强 |
| 2 | Nano 0.5.1 | `D:\goserver\nano-0.5.1\nano-0.5.1` | 轻量级，基于 Session |
| 3 | MQant 1.5.3 | `D:\goserver\mqant-1.5.3\mqant-1.5.3` | 微服务 + RPC |
| 4 | Leaf 1.1.3 | `D:\goserver\leaf-1.1.3\leaf-1.1.3` | 简洁，最小化 |
| 5 | Cherry 1.5.1 | `D:\goserver\cherry-1.5.1\cherry-1.5.1` | 基于组件 |

## 编码规范

- **注释语言：** 所有注释、日志消息、错误消息使用简体中文。
- **命名规范：** 遵循 Google Go 命名规范。
- **包组织：** 基础接口和类型抽离到独立包。各概念归属规则：
  - `config/` — Config 结构体 + Option 函数式选项（全部5个框架均独立）
  - `constants/` — 哨兵错误变量（Pitaya 模式，`package constants`）
  - `errors/` — 自定义错误类型（Pitaya 模式，`package errors`）
  - `component/` — Component 接口 + Base 空实现（Pitaya/Nano/Cherry 独立）
  - `log/` — 日志全局单例 + 便捷函数（全部5个框架均独立）
  - `app/` — 仅保留 Application 生命周期、Builder 构建器、State 状态枚举（框架入口）
- **名词对齐：** 核心类型和包的命名优先与多数参考框架保持一致。统计 5 个框架中各用何名，取多数派；平局时优先 Pitaya/Nano/Cherry（三者风格与本项目最接近）。

## 任务执行指南

收到任务时（例如"编写 application 启动骨架"）：

- 并行地在全部 5 个框架中搜索相关文件：使用 `Grep "func.*[Aa]pp"` 或 `Glob "**/app*.go"` 在每个框架中查找。
- 读取找到的关键文件，总结具体优缺点。
- 仅在分析完成后，提出并编写实现方案。
