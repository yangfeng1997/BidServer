# CLAUDE.md

本文件是 `config/` 的局部索引。进入配置目录工作时，先读本文件，再进入 schema / gen / values / secrets 对应子目录。

> **维护约定**：本文件只记录配置链路的分层与导航；当 schema、生成代码、值文件或 secrets 文件变更时，同步更新索引。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 配置源、配置生成产物和环境值目录。
- `schema/` 是事实源，`gen/` 是生成产物，`values/` 是环境取值，`secrets/` 是示例密钥输入。

## 子目录

- [`gen/`](gen/)
- [`schema/`](schema/)
- [`secrets/`](secrets/)
- [`values/`](values/)

## 主要文件

- [`common.yaml`](common.yaml)
- [`gate.yaml`](gate.yaml)
- [`lobby.yaml`](lobby.yaml)

## 快速读法

- 改配置类型先看 `config/schema/`。
- 改生成逻辑先看 `config/gen/` 的生成入口和测试。
- 改环境值先看 `config/values/`。
- 改运行时注入说明先看 `config/secrets/`。

## 工作规则

- `schema/` 优先于 `gen/`；改 schema 后要回看生成链路。
- `gen/` 是生成代码，不能手改。
- `values/` 控制环境值，`svr_list` 是服务列表入口。
- `secrets/` 只放示例或说明，不放真实密钥。
