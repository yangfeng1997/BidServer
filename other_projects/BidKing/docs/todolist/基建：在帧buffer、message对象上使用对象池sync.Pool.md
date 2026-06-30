每消息 5-6 次堆分配 — 这是最大的性能短板。高 QPS 下 GC 会成为主要瓶颈,P99 延迟被 GC 暂停拉高。成熟框架(如 gnet 系、pitaya)在帧 buffer、message 对象上都会用 sync.Pool。

- 要补(按数据):帧 buffer 上 sync.Pool、handler 默认 protobuf、反射调用缓存。强调:等 benchmark 确认瓶颈再做,别盲目优化。
