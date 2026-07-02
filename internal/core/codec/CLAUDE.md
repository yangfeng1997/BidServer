# CLAUDE.md

本文件是 `internal/core/codec/` 的局部索引。进入协议编解码目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`codec`。
- 外层连接帧 `Packet` 与内层业务消息 `Message` 的编码 / 解码。

## 主要文件

- [`packet.go`](packet.go)
- [`message.go`](message.go)
- [`codec_test.go`](codec_test.go)

## 快速读法

- 查外层网络包格式看 `packet.go`。
- 查内层消息格式、请求 / 响应 / 通知看 `message.go`。

## 工作规则

- 修改二进制格式会影响网关、连接和客户端协议，必须同步测试和协议文档。
