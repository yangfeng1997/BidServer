# CLAUDE.md

本文件是 `tools/client_robot/` 的局部索引。进入客户端模拟工具目录时，先读本文件，再看 `main.go`。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- Go package：`main`。
- 用于开发期模拟客户端连接 gatesvr，按 `internal/core/codec` 的 Packet / Message 格式发送协议。

## 主要文件

- [`main.go`](main.go)

## 快速读法

- `main.go` 默认执行 handshake，然后发送 `CS_Ping_Req`，等待 `SC_Pong_Rsp`。
- 默认 TCP 地址为 `127.0.0.1:7001`，可用 `--addr` 覆盖。

## 工作规则

- 只作为手工联调工具，不引入服务端运行依赖。
- 修改客户端协议 cmd_id 时，同步更新本工具里的默认请求。
