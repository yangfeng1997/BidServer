# 单机单 RouterAgent 压测结果

## 测试环境

- 日期：2026-07-06
- 模式：单机单 RouterAgent，本地 UDS 转发，不经过 RA 间 TCP
- 客户端入口：`127.0.0.1:7001`
- 拓扑：`client -> gatesvr -> RouterAgent(UDS) -> lobbysvr`

| 进程 | nodeid | UDS / listen | pprof |
|---|---|---|---|
| RouterAgent | `77.6.1` | `/run/routeragent/ra.sock`, `127.0.0.1:7100` | `127.0.0.1:6061` |
| gatesvr | `77.1.1` | connect `/run/routeragent/ra.sock`, listen `127.0.0.1:7001` | `127.0.0.1:6063` |
| lobbysvr | `77.2.1` | connect `/run/routeragent/ra.sock` | `127.0.0.1:6064` |

## 压测结果

| clients | requests/client | sent | ok | failed | reconnects | elapsed | QPS | avg | p50 | p90 | p99 | max |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 1000 | 100000 | 100000 | 0 | 0 | 1.277s | 78298.96 | 1.208ms | 1.022ms | 2.055ms | 4.233ms | 25.716ms |
| 200 | 1000 | 200000 | 200000 | 0 | 0 | 1.656s | 120728.60 | 1.545ms | 1.303ms | 2.614ms | 5.083ms | 48.981ms |
| 500 | 500 | 250000 | 250000 | 0 | 0 | 1.203s | 207777.99 | 2.143ms | 1.874ms | 3.418ms | 6.081ms | 98.742ms |
| 1000 | 300 | 300000 | 300000 | 0 | 0 | 1.190s | 252054.11 | 3.344ms | 2.873ms | 5.419ms | 8.722ms | 76.398ms |

原始 JSON 结果曾保存于临时目录 `/tmp/bidserver_bench_single/`，本文件为本次结果归档。

## RouterAgent Metrics 快照

```text
routeragent_peer_connect_fail_total 0
routeragent_forward_total 1700002
routeragent_remote_seq_timeout 0
routeragent_unknown_seq 0
routeragent_remote_seq_pending 0
routeragent_waiter_count 0
routeragent_remote_seq_pending_current 0
routeragent_peer_disconnect_total 0
routeragent_late_response 0
routeragent_peer_disconnect_drop 0
routeragent_route_miss 0
routeragent_incoming_peer_seq 0
routeragent_peer_connect_total 0
```

## 与本机双 RA 结果对比

| clients | 单 RA QPS | 双 RA QPS | 单 RA 提升 | 单 RA p99 | 双 RA p99 |
|---:|---:|---:|---:|---:|---:|
| 100 | 78298.96 | 70424.74 | 11.18% | 4.233ms | 3.950ms |
| 200 | 120728.60 | 107659.92 | 12.14% | 5.083ms | 4.870ms |
| 500 | 207777.99 | 181626.43 | 14.40% | 6.081ms | 6.386ms |
| 1000 | 252054.11 | 230669.91 | 9.27% | 8.722ms | 12.870ms |

## 结论

- 四档压测全部 `failed=0`、`reconnects=0`。
- 单 RA 模式最高约 `252k QPS`，高于本机双 RA 的约 `230k QPS`。
- 相比本机双 RA，单 RA 少了一跳 RA 间 TCP、peer 队列和 remote seq 映射，QPS 提升约 `9% - 14%`。
- `peer_connect_total=0` 符合预期，说明本次是纯本地 UDS 投递，没有跨 RA peer 连接。
