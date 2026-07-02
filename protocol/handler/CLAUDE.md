# CLAUDE.md

本文件是 `protocol/handler/` 的局部索引。进入前端 handler 协议目录工作时，先读本文件，再看 proto 源和生成代码。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 面向客户端入口的 handler service 定义。
- 当前仅保留已有服务相关协议：lobby。

## 主要文件

- [`lobby_handler.proto`](lobby_handler.proto)

## 工作规则

- 修改 handler service 后要重新生成 `protocol/gen/routes.go` 和 `protocol/gen/handler/`。
- 没有对应服务实现的 handler 协议不要引入当前主链路。
