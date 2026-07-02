# CLAUDE.md

本文件是 `tools/gen_routes/` 的局部索引。进入路由生成工具目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`main`。
- 扫描 `protocol/handler/*.proto` 中的 cmd_id、server_type、no_auth 等扩展选项，生成 `protocol/gen/routes.go`。

## 主要文件

- [`main.go`](main.go)
- [`main_test.go`](main_test.go)

## 快速读法

- 查输入扫描和 proto 解析看 `main.go`。
- 查生成结果格式看测试中的 golden 断言。

## 工作规则

- 只修改生成规则，不手改 `protocol/gen/routes.go` 的业务语义。
- 扩展选项字段变化时要同步 `protocol/common/options.proto`。
