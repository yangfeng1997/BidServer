# CLAUDE.md

本文件为 Claude Code (claude.ai/code) 在此仓库中工作时提供指引。它只保留**索引**与**必要项**（工程纪律、工作流、规则治理）；架构、命令、代码放置等详细参考拆到根目录的 `architecture.md`（及其细分 `network.md` / `cluster.md`）/ `development.md` / `code_style.md` 子文档（专供 Claude 按需加载），按需加载。

> **维护约定**：凡是对本文件或根目录下 `architecture.md` / `network.md` / `cluster.md` / `development.md` / `code_style.md` 子文档已记录内容有影响的变更，都必须同步更新对应文件。包括但不限于：新增/删除/移动目录或包、修改架构设计或接口定义、新增/变更构建与测试命令、调整配置结构或部署方式。保持文档与代码库实际状态一致。

---

## 文档索引

| 文档 | 内容 | 何时读 |
|---|---|---|
| 本文件 `CLAUDE.md` | 文档索引、工程纪律、工作流、规则优先级、输出语言、上下文加载 | 始终（每次都加载） |
| [`architecture.md`](architecture.md) | 架构概览（目录树）、分发主干（配置/应用框架层/消息路由/Handler）、gate 转发完整流程 | 动手写框架/业务代码前 |
| [`network.md`](network.md) | 网络与连接层：消息协议（两层帧）、握手协议、Agent、Session | 写网络/连接相关代码前 |
| [`cluster.md`](cluster.md) | 集群与跨进程：cluster RPC（包结构/NodeID/discovery/transport/ICluster/ctx 工具）、TaskQueue | 写集群/RPC/跨进程相关代码前 |
| [`development.md`](development.md) | 常用命令（构建/测试/protoc/gen_routes）、工程目录与代码放置规则、新增 protobuf 协议 / 配置的步骤 | 构建测试、新增代码、扩展协议或配置时 |
| [`code_style.md`](code_style.md) | Go 代码命名规范（遵循 Google Go Style Guide） | 命名与编码风格存疑时 |

**基础事实**：

- 模块名为 `project`（见 `go.mod`），导入路径以此为前缀，例如 `project/src/common/logger`。
- 命名规范见 `code_style.md`；构建只需 `go build ./...`，测试只需 `go test ./...`（更多命令见 `development.md`）。

---

## 输出语言

- 生成的文档、代码注释、本文件：简体中文。
- 对话回复：简体中文。

---

## 规则优先级

规则冲突时：

1. `code_style.md` 管命名规范；本文件管工程纪律、工作流与规则治理；架构、命令、代码放置等参考见根目录的 `architecture.md`（细分 `network.md` / `cluster.md`）/ `development.md` 子文档。
2. 更具体的规则优先于更宽泛的规则。
3. 仍不明确时，停下来问，不要默默猜测。

---

## 工程纪律

### 实现

- 校验所有外部输入（客户端 / RPC / 配置 / 缓存数据）。
- 检查对象的存在性、归属、生命周期、可操作状态与状态转移合法性。
- 处理：非法参数、缺失配置、脏数据、重试、重复请求、重入、并发、乱序事件。这是并发的网络集群服务，注意守护共享状态。
- 发奖、扣资源、结算、落库必须**幂等**，绝不静默重复；保持内存 / 缓存 / DB / 异步回调一致。
- 关键失败路径要有显式错误处理与有用日志（用 `logger` 的强类型字段），不要只写 happy-path 代码。

### 清理

- 清掉自己改动产生的孤儿：新出现的未用 import、变量、函数。
- 既有死代码不主动删（除非被要求），提出来即可。

### 沟通

- 明确说明假设、其他可能的解读、以及更简单的替代方案。
- 不清楚就停下来问。
- **终稿前自检**：输入校验、状态检查、幂等 / 重入、并发安全、一致性、测试风险。

---

## 工作流

### 版本控制

- 所有代码改动走 feature 分支 + Pull Request 合入 `main`，**禁止直接在 `main` 上提交**。
- 分支命名沿用现有风格 `<type>/<kebab-desc>`，如 `feat/...`、`fix/...`、`refactor/...`、`docs/...`、`chore/...`、`test/...`。
- 提交信息用 Conventional Commits + 中文描述，如 `feat: 框架雏形`。
- 若误提交到本地 `main`（尚未 push）：

  ```bash
  git checkout -b <new-branch>         # 把提交挪到新分支
  git branch -f main origin/main       # 本地 main 重置回远端
  ```

  移动提交，别丢掉。

### 测试

- 测试从设计意图 / 文档推导，**不要**从源码反推。
- 当约定 / 文档与实现冲突导致测试失败时，先暂停并报告：失败原因、源码位置、文档依据。
- **不要**为迁就实现去改测试；只有 mock / 断言本身有 bug 才是改测试的正当理由。
- 报告测试中发现的行为偏差、隐藏假设、边界异常，即使超出当前任务范围。

---

## 上下文加载

- 按任务阶段增量加载上下文，**不要**一次性预读全部文档。
- 动手前先读 `architecture.md`（架构概览 / 分发主干）与 `code_style.md`；写网络/连接代码再读 `network.md`，写集群/RPC/跨进程代码再读 `cluster.md`；构建测试、新增代码或扩展协议/配置时再读 `development.md`。
- 新增代码前，先读最近的同类包（`logger` / `config` / `framework` 等）并对齐其风格。
- 文档不足时才读源码，不要默认通读源码。
