# 本机双 RA 压测结果

## 测试环境

- 日期：2026-07-06
- 模式：本机双 RouterAgent 模拟跨节点转发
- 客户端入口：`127.0.0.1:7001`
- 拓扑：`client -> gatesvr -> RA1(UDS) -> RA1/RA2(TCP loopback) -> RA2(UDS) -> lobbysvr`

| 进程 | nodeid | UDS / listen | pprof |
|---|---|---|---|
| RA1 | `77.6.1` | `/tmp/ra1.sock`, `127.0.0.1:7100` | `127.0.0.1:6061` |
| RA2 | `77.6.2` | `/tmp/ra2.sock`, `127.0.0.1:7101` | `127.0.0.1:6062` |
| gatesvr | `77.1.1` | connect `/tmp/ra1.sock`, listen `127.0.0.1:7001` | `127.0.0.1:6063` |
| lobbysvr | `77.2.1` | connect `/tmp/ra2.sock` | `127.0.0.1:6064` |

## 压测结果

| clients | requests/client | sent | ok | failed | reconnects | elapsed | QPS | avg | p50 | p90 | p99 | max |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 1000 | 100000 | 100000 | 0 | 0 | 1.419s | 70424.74 | 1.364ms | 1.207ms | 2.165ms | 3.950ms | 17.524ms |
| 200 | 1000 | 200000 | 200000 | 0 | 0 | 1.857s | 107659.92 | 1.757ms | 1.541ms | 2.823ms | 4.870ms | 56.464ms |
| 500 | 500 | 250000 | 250000 | 0 | 0 | 1.376s | 181626.43 | 2.450ms | 2.178ms | 3.817ms | 6.386ms | 84.446ms |
| 1000 | 300 | 300000 | 300000 | 0 | 0 | 1.300s | 230669.91 | 3.717ms | 3.248ms | 5.675ms | 12.870ms | 119.486ms |

原始 JSON 结果曾保存于临时目录 `/tmp/bidserver_bench/`，本文件为本次结果归档。

## RouterAgent Metrics 快照

RA1：

```text
routeragent_peer_disconnect_total 0
routeragent_forward_total 2550003
routeragent_waiter_count 0
routeragent_peer_127_0_0_1_7101_2_send_cap 16384
routeragent_peer_connect_total 1
routeragent_remote_seq_timeout 0
routeragent_peer_disconnect_drop 0
routeragent_incoming_peer_seq 0
routeragent_peer_127_0_0_1_7101_2_send_max 440
routeragent_peer_127_0_0_1_7101_2_prio_len 0
routeragent_peer_127_0_0_1_7101_2_prio_cap 4096
routeragent_unknown_seq 0
routeragent_route_miss 0
routeragent_remote_seq_pending 0
routeragent_peer_127_0_0_1_7101_2_send_len 0
routeragent_peer_127_0_0_1_7101_2_prio_max 0
routeragent_peer_connect_fail_total 0
routeragent_late_response 0
routeragent_remote_seq_pending_current 0
```

RA2：

```text
routeragent_peer_127_0_0_1_7100_2_send_cap 16384
routeragent_peer_127_0_0_1_7100_2_send_max 0
routeragent_peer_disconnect_total 0
routeragent_forward_total 850001
routeragent_peer_127_0_0_1_7100_2_send_len 0
routeragent_peer_127_0_0_1_7100_2_prio_len 0
routeragent_peer_127_0_0_1_7100_2_prio_cap 4096
routeragent_late_response 0
routeragent_peer_disconnect_drop 0
routeragent_route_miss 0
routeragent_incoming_peer_seq 0
routeragent_peer_connect_total 1
routeragent_unknown_seq 0
routeragent_remote_seq_pending_current 0
routeragent_peer_127_0_0_1_7100_2_prio_max 478
routeragent_peer_connect_fail_total 0
routeragent_remote_seq_timeout 0
routeragent_remote_seq_pending 0
routeragent_waiter_count 0
```

## 结论

- 四档压测全部 `failed=0`、`reconnects=0`。
- `remote_seq_pending=0`、`route_miss=0`、`unknown_seq=0`、`late_response=0`，说明本次跨 RA 请求和响应链路没有遗留未回收 seq，也没有路由丢失。
- 1000 clients 档达到约 `230k QPS`，p99 约 `12.87ms`，最大延迟约 `119.49ms`。

## 注意事项

- 两个 RouterAgent 的 `listen_addr`、`--pprof-addr`、`sock_path`、pid 文件和日志 basename 必须互不重复。
- RouterAgent 自身注册使用 CAS，同 nodeid 重启前要等旧 etcd lease 过期，避免 `etcd node already exists`。
- 共享 etcd 曾出现 lease keepalive 断开导致前缀 `count=0` 的问题；当前 RouterAgent 使用周期性 `KeepAliveOnce`，失败时会重新申请 lease 并重写已注册节点。
- 压测前建议先确认 etcd 前缀下存在 RA1、RA2、gate、lobby 四个 key，再跑矩阵。
