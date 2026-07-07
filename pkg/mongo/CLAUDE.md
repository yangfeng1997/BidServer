# CLAUDE.md

本文件是 `pkg/mongo/` 的局部索引。进入 Mongo 公共库时，先读本文件，再看 `mongo.go`。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- MongoDB 直连封装。
- 阻塞 Mongo IO 在 off-loop goroutine 执行，完成后通过 `taskqueue.TaskQueue` 投递回主循环。
- 适合 lobby 这类单业务线程模块使用，避免在主循环直接阻塞数据库调用。

## 主要文件

- [`mongo.go`](mongo.go)：连接管理与异步 CRUD。
- [`mongo_test.go`](mongo_test.go)：方法签名与回调投递测试。

## 工作规则

- `pkg/mongo` 不依赖具体服务，只依赖 `pkg/taskqueue` 的 `TaskQueue` 接口。
- 新增数据库操作时保持回调投递语义：IO goroutine 执行，done 回到调用方主循环。
- 不在测试中要求真实 Mongo，除非测试显式标成集成测试并可跳过。
