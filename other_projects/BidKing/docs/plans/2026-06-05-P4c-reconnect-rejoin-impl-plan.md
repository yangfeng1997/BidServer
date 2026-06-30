# P4c 重连接回 + 改投 + room-death 探测 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把对局弧加固成能容忍掉线/重连/换 lobby/room 崩溃的健壮闭环——5min 重连接回原拍卖局、room participant `LobbyNodeID` 改投、惰性 room-death 探测。

**Architecture:** 权威重连源是 onlinesvr 在线目录（5min TTL 宽限窗）。`Directory.Register` 保留 room 绑定、`Runtime.Disconnect` 对 in-game 玩家跳过注销让条目靠 TTL 过期。重连时 lobby `Login` 查 online 拿绑定 → off-loop 调 `RoomHandler.rejoin`（room 主循环原子改投 participant lobbyNode + 回拍卖快照）→ 重建内存亲和 + 推 `SC_ReconnectAuction`；room 死则作废清亲和。大厅消费命中亲和时惰性探活 `RoomHandler.querygame`。

**Tech Stack:** Go；NATS+etcd 集群 RPC（发送端 `proto.Marshal`，经 routersvr DIRECT/CONSISTENT_HASH 寻址）；protobuf wire；lobby/room 单主循环（lobby taskqueue 串行、room 帧驱动 timewheel）；TDD（沙箱单测 + `//go:build integration` 仅编译验证，无 Docker）。

> 设计依据：[`2026-06-05-P4c-reconnect-rejoin.md`](2026-06-05-P4c-reconnect-rejoin.md)。承接 P4a/P4b（PR #21/#24，已合入 main）。

---

## 设计要点（动手前必读）

- **反射路由注册**：`app.RegisterHandler(NewRoomHandler(rt), nil)`（`roomsvr/main.go`）反射扫描 handler 所有方法，以 `RoomHandler.<方法名小写>` 注册 route。新增方法 `Rejoin`/`Querygame` → 自动注册 `RoomHandler.rejoin`/`RoomHandler.querygame`，**无需改 main.go**。
- **跨服 off-loop hook 模式**（lobby `Runtime`）：跨服网络调用封 `xxxViaRouter`（`go func(){ CallViaSync; done }`），赋给可替换 hook 字段（默认在 `NewRuntime` 的 `cfg.Cluster != nil` 块内接真实实现，单测注 fake）。状态变更在 done 回调内 `rt.Submit` 回主循环。**hook 字段须经 `runOnLoop` 在主循环内赋值**（否则 `-race` 报数据竞争）。
- **room 帧驱动主循环**：`RoomHandler` 薄壳 `Submit` 进主循环 + `ErrDeferredReply`；改投与 `settle`（timewheel 回调 inline）在同一 goroutine 串行，可零锁改 `Game`。
- **`SC_ReconnectAuction` 是 push-only**（仅 `msg_id`，不带 `server_type`/`handler_method`），同 `SC_AuctionState`；走 `gen_routes` 客户端路由表，非 handler route。
- **客户端全链路 proto**：推送 body 用 `clientSerializer = protobuf.NewSerializer()`（presence.go）。
- **protoc 借用**（系统未装，见 development.md / 记忆 gameserver-dev-workflow）：
  ```bash
  PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc
  PROTO_INCLUDE=/game/dev/silver-server/3rd/protobuf/include
  ```
  生成命令带 `--proto_path=.`（lobby.proto 依赖 protocal/options.proto），**勿用 tools/gen_proto.sh**（缺 `--proto_path=.`）。

---

## File Structure

| 文件 | 责任 | 动作 |
|---|---|---|
| `protocal/room.proto` | 新增 `RPC_Rejoin_Req/Rsp`、`RPC_QueryGame_Req/Rsp` | 改 |
| `protocal/lobby.proto` | 新增 `SC_ReconnectAuction`（msg_id 2042） | 改 |
| `protocal/gen/{room,lobby}/*.pb.go`、`protocal/gen/routes/routes.go` | protoc + gen_routes 生成产物 | 生成 |
| `src/servers/onlinesvr/internal/directory.go` | `Register` 保留已存在条目 room 绑定 | 改 |
| `src/servers/roomsvr/internal/runtime.go` | `Runtime.Rejoin`（改投+快照）、`Runtime.QueryGame`（判活） | 改 |
| `src/servers/roomsvr/internal/room_handler.go` | `RoomHandler.Rejoin`、`RoomHandler.Querygame` 薄壳 | 改 |
| `src/servers/lobbysvr/internal/presence.go` | `pushReconnectAuction` + msgID 常量 | 改 |
| `src/servers/lobbysvr/internal/runtime.go` | reconnect hook（queryOnline/rejoinRoom/queryGame）+ ViaRouter + `tryReconnect` + `PushReconnectAuction` + `Login` 重连分支 + `Disconnect` in-game 语义 | 改 |
| `src/servers/lobbysvr/internal/lobby_handler.go` | `Purchase` 惰性探活 | 改 |
| `src/servers/lobbysvr/internal/reconnect_integration_test.go` | 重连端到端集成骨架（build-tag，t.Skip） | 建 |
| `architecture.md` / `cluster.md` / `development.md` | 同步重连流程/改投/新 route/新 msg_id | 改 |

各服务集成测试放各自包内（Go internal 可见性）。

---

## Stage A — proto + gen_routes

### Task A1: 扩 room/lobby proto + 重生成

**Files:**
- Modify: `protocal/room.proto`
- Modify: `protocal/lobby.proto:261`（文件末尾追加）
- Generate: `protocal/gen/room/*.pb.go`、`protocal/gen/lobby/*.pb.go`、`protocal/gen/routes/routes.go`

- [ ] **Step 1: 在 `protocal/room.proto` 末尾（第 58 行 `}` 之后）追加 Rejoin/QueryGame 消息**

```proto

// RPC_Rejoin_Req lobby → room 重连改投（经 router DIRECT key=room_node_id，route="RoomHandler.rejoin"）
message RPC_Rejoin_Req {
  string game_id        = 1;
  int64  uid            = 2;
  string new_lobby_node = 3; // 重连后新 lobby NodeID 串，room 据此改投 participant
}
message RPC_Rejoin_Rsp {
  int32  code                = 1; // 0=接回（存在且未封盘）；1=已封盘；2=局不存在/非参与者
  int64  highest_bid         = 2;
  int64  highest_bidder      = 3;
  int32  countdown_remaining = 4;
  int32  item_id             = 5;
  string currency            = 6;
}

// RPC_QueryGame_Req lobby → room 只读判活（经 router DIRECT key=room_node_id，route="RoomHandler.querygame"）
message RPC_QueryGame_Req {
  string game_id = 1;
}
message RPC_QueryGame_Rsp {
  bool exists = 1;
  bool closed = 2;
}
```

- [ ] **Step 2: 在 `protocal/lobby.proto` 末尾（第 261 行 `}` 之后）追加 SC_ReconnectAuction**

```proto

// SC_ReconnectAuction 重连接回拍卖态快照 / 作废通知（仅 msg_id，gate 据此推）
message SC_ReconnectAuction {
  option (options.msg_id) = 2042;
  string game_id             = 1;
  int64  highest_bid         = 2;
  int64  highest_bidder      = 3;
  int32  countdown_remaining = 4;
  int32  item_id             = 5;
  string currency            = 6;
  int32  status              = 7; // 0=active 接回快照；1=voided 作废
}
```

- [ ] **Step 3: 重生成 pb + 路由表**

Run:
```bash
cd /game/GameServer && \
PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc && \
PROTO_INCLUDE=/game/dev/silver-server/3rd/protobuf/include && \
$PROTOC --go_out=. --go_opt=module=project --proto_path=. --proto_path=$PROTO_INCLUDE protocal/room.proto && \
$PROTOC --go_out=. --go_opt=module=project --proto_path=. --proto_path=$PROTO_INCLUDE protocal/lobby.proto && \
go run ./tools/gen_routes
```
Expected: 无报错；`protocal/gen/room/room.pb.go` 含 `RPC_Rejoin_Req`/`RPC_QueryGame_Req`，`protocal/gen/lobby/lobby.pb.go` 含 `SC_ReconnectAuction`。

- [ ] **Step 4: 验证生成产物**

Run: `grep -l "RPC_Rejoin_Req\|RPC_QueryGame_Req" protocal/gen/room/*.go && grep -l "SC_ReconnectAuction" protocal/gen/lobby/*.go && grep "2042" protocal/gen/routes/routes.go`
Expected: 三处均命中（room pb / lobby pb / routes 含 2042）。

- [ ] **Step 5: 全量编译**

Run: `cd /game/GameServer && go build ./...`
Expected: PASS（无未用引用——生成代码不影响既有调用）。

- [ ] **Step 6: Commit**

```bash
git add protocal/room.proto protocal/lobby.proto protocal/gen/
git commit -m "feat(proto): P4c 新增 room rejoin/querygame + lobby SC_ReconnectAuction(2042)"
```

---

## Stage B — onlinesvr：Register 保留 room 绑定

### Task B1: `Directory.Register` 保留已存在条目的 RoomNodeID/GameID

**Files:**
- Modify: `src/servers/onlinesvr/internal/directory.go:45-64`
- Test: `src/servers/onlinesvr/internal/directory_test.go`

- [ ] **Step 1: 写失败测试（重注册不抹 room 绑定；TTL 仍随条目过期）**

追加到 `src/servers/onlinesvr/internal/directory_test.go`：

```go
func TestDirectory_RegisterPreservesRoomBinding(t *testing.T) {
	tw := timewheel.New(time.Millisecond, 64)
	dir := NewDirectory(tw, time.Second)
	dir.Register(7, "1.1.1", "1.2.1", time.Now().UnixNano())
	if !dir.BindRoom(7, "1.7.1", "1.8.1-1") {
		t.Fatalf("BindRoom should succeed")
	}
	// 重连：换 gateway/lobby 重注册（顶号路径）——room 绑定必须保留
	old, replaced := dir.Register(7, "1.1.2", "1.2.2", time.Now().UnixNano())
	if !replaced || old == nil {
		t.Fatalf("cross-gateway re-register should return old/replaced")
	}
	e, ok := dir.Query(7)
	if !ok || e.GatewayNodeID != "1.1.2" || e.LobbyNodeID != "1.2.2" {
		t.Fatalf("re-register should update gate/lobby, got %+v", e)
	}
	if e.RoomNodeID != "1.7.1" || e.GameID != "1.8.1-1" {
		t.Fatalf("re-register must PRESERVE room binding, got room=%q game=%q", e.RoomNodeID, e.GameID)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd /game/GameServer && go test ./src/servers/onlinesvr/internal/ -run TestDirectory_RegisterPreservesRoomBinding -v`
Expected: FAIL（`room binding` 断言失败：当前 Register 建空 Entry，room 字段被清为 ""）。

- [ ] **Step 3: 实现——Register 沿用旧条目 room 绑定**

将 `src/servers/onlinesvr/internal/directory.go:45-64` 的 `Register` 改为：

```go
func (d *Directory) Register(uid int64, gw, lobby string, nowNano int64) (old *Entry, replaced bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var roomNodeID, gameID string // room 绑定与 gate/lobby 位置正交，重注册保留（P4c-2）
	if e, ok := d.entries[uid]; ok {
		roomNodeID, gameID = e.RoomNodeID, e.GameID
		if e.GatewayNodeID != gw {
			cp := *e
			old, replaced = &cp, true
		}
		if t := d.timers[uid]; t != nil {
			d.tw.Stop(t)
		}
	}
	d.entries[uid] = &Entry{
		Uid: uid, GatewayNodeID: gw, LobbyNodeID: lobby,
		LoginTime: nowNano, LastActive: nowNano,
		RoomNodeID: roomNodeID, GameID: gameID,
	}
	d.timers[uid] = d.scheduleExpire(uid)
	return old, replaced
}
```

- [ ] **Step 4: 运行验证通过 + 既有目录测试不回归**

Run: `cd /game/GameServer && go test ./src/servers/onlinesvr/internal/ -run TestDirectory -race -v`
Expected: PASS（新用例 + 既有 `TestDirectory_*` 全绿，含顶号/过期/StaleExpire）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/onlinesvr/internal/directory.go src/servers/onlinesvr/internal/directory_test.go
git commit -m "feat(online): Register 保留 room 绑定（重连宽限窗前置，P4c）"
```

---

## Stage C — roomsvr：rejoin 改投 + querygame 判活

### Task C1: `Runtime.Rejoin`（改投 participant lobbyNode + 回快照）

**Files:**
- Modify: `src/servers/roomsvr/internal/runtime.go`（在 `Game()` 访问器 `runtime.go:239` 前后追加）
- Test: `src/servers/roomsvr/internal/runtime_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `src/servers/roomsvr/internal/runtime_test.go`：

```go
func TestRuntime_Rejoin(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	defer rt.Stop()
	runOnLoop(t, rt, func() {
		rt.OpenGame("g1", 7, 30, "gold", []Participant{{UID: 1, LobbyNodeID: "1.2.1"}, {UID: 2, LobbyNodeID: "1.2.1"}})
		rt.Bid("g1", 1, 100)
	})
	// 接回：改投 uid=1 到新 lobby 1.2.9 + 回当前快照
	var code int32
	var hb, hbr int64
	var rem, itemID int32
	var currency string
	var newLobby string
	runOnLoop(t, rt, func() {
		code, hb, hbr, rem, itemID, currency = rt.Rejoin("g1", 1, "1.2.9")
		newLobby = rt.Game("g1").Participants[0].LobbyNodeID
	})
	if code != 0 || hb != 100 || hbr != 1 || itemID != 7 || currency != "gold" {
		t.Fatalf("rejoin alive snapshot mismatch: code=%d hb=%d hbr=%d item=%d cur=%s rem=%d", code, hb, hbr, itemID, currency, rem)
	}
	if newLobby != "1.2.9" {
		t.Fatalf("rejoin must re-route participant lobbyNode to 1.2.9, got %q", newLobby)
	}
	// 局不存在 → code 2；非参与者 → code 2
	runOnLoop(t, rt, func() {
		if c, _, _, _, _, _ := rt.Rejoin("nope", 1, "1.2.9"); c != 2 {
			t.Fatalf("absent game should be code 2, got %d", c)
		}
		if c, _, _, _, _, _ := rt.Rejoin("g1", 999, "1.2.9"); c != 2 {
			t.Fatalf("non-participant should be code 2, got %d", c)
		}
	})
}

func TestRuntime_RejoinClosed(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	defer rt.Stop()
	runOnLoop(t, rt, func() {
		rt.OpenGame("g1", 7, 30, "gold", []Participant{{UID: 1, LobbyNodeID: "1.2.1"}})
		rt.Game("g1").closed = true // 模拟已封盘
	})
	runOnLoop(t, rt, func() {
		if c, _, _, _, _, _ := rt.Rejoin("g1", 1, "1.2.9"); c != 1 {
			t.Fatalf("closed game rejoin should be code 1, got %d", c)
		}
	})
}
```

> 注：`runOnLoop` 已在 `runtime_test.go` 既有（settle/bid 测试用）。若该 helper 在 roomsvr 测试中尚不存在，复用 `Submit`+chan barrier（见既有 room 测试同款）。

- [ ] **Step 2: 运行验证失败**

Run: `cd /game/GameServer && go test ./src/servers/roomsvr/internal/ -run TestRuntime_Rejoin -v`
Expected: FAIL（`rt.Rejoin` undefined）。

- [ ] **Step 3: 实现 `Runtime.Rejoin`**

在 `src/servers/roomsvr/internal/runtime.go` 的 `Game()` 方法（文件末尾 `runtime.go:239`）之前插入：

```go
// Rejoin 主循环内重连改投：把 uid 的 participant LobbyNodeID 改为 newLobbyNode，回当前拍卖快照。
// code：0=接回（存在且未封盘）；1=已封盘；2=局不存在/非参与者。
func (rt *Runtime) Rejoin(gameID string, uid int64, newLobbyNode string) (int32, int64, int64, int32, int32, string) {
	g := rt.games[gameID]
	if g == nil || !g.isParticipant(uid) {
		return 2, 0, 0, 0, 0, ""
	}
	if g.closed {
		return 1, 0, 0, 0, 0, ""
	}
	for i := range g.Participants {
		if g.Participants[i].UID == uid {
			g.Participants[i].LobbyNodeID = newLobbyNode
			break
		}
	}
	return 0, g.HighestBid, g.HighestBidder, g.remaining(), g.ItemID, g.Currency
}
```

- [ ] **Step 4: 运行验证通过**

Run: `cd /game/GameServer && go test ./src/servers/roomsvr/internal/ -run TestRuntime_Rejoin -race -v`
Expected: PASS（`TestRuntime_Rejoin` + `TestRuntime_RejoinClosed`）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/roomsvr/internal/runtime.go src/servers/roomsvr/internal/runtime_test.go
git commit -m "feat(room): Runtime.Rejoin 改投 participant lobbyNode + 回拍卖快照（P4c）"
```

### Task C2: `RoomHandler.Rejoin` 薄壳

**Files:**
- Modify: `src/servers/roomsvr/internal/room_handler.go`（在 `Opengame` 后追加）
- Test: `src/servers/roomsvr/internal/room_handler_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `src/servers/roomsvr/internal/room_handler_test.go`：

```go
func rejoinSync(t *testing.T, rt *Runtime, req *roompb.RPC_Rejoin_Req) *roompb.RPC_Rejoin_Rsp {
	t.Helper()
	r := newCapReplier()
	ctx := cluster.WithReplier(cluster.WithSession(context.Background(), &clusterpb.ClusterSession{}), r)
	h := NewRoomHandler(rt)
	if _, err := h.Rejoin(ctx, req); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	select {
	case res := <-r.ch:
		if res.err != nil {
			t.Fatalf("rejoin err: %v", res.err)
		}
		var out roompb.RPC_Rejoin_Rsp
		if err := proto.Unmarshal(res.data, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return &out
	case <-time.After(2 * time.Second):
		t.Fatalf("rejoin timeout")
		return nil
	}
}

func TestRoomHandler_Rejoin(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	defer rt.Stop()
	openGameSync(t, rt, &roompb.RPC_OpenGame_Req{
		GameId: "g1", ItemId: 7, CountdownSec: 30, Currency: "gold",
		Participants: []*roompb.Participant{{Uid: 1, LobbyNodeId: "1.2.1"}},
	})
	rsp := rejoinSync(t, rt, &roompb.RPC_Rejoin_Req{GameId: "g1", Uid: 1, NewLobbyNode: "1.2.9"})
	if rsp.Code != 0 || rsp.ItemId != 7 || rsp.Currency != "gold" {
		t.Fatalf("rejoin handler should return alive snapshot, got %+v", rsp)
	}
	// 局不存在 → code 2
	if bad := rejoinSync(t, rt, &roompb.RPC_Rejoin_Req{GameId: "nope", Uid: 1, NewLobbyNode: "1.2.9"}); bad.Code != 2 {
		t.Fatalf("absent game should be code 2, got %d", bad.Code)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd /game/GameServer && go test ./src/servers/roomsvr/internal/ -run TestRoomHandler_Rejoin -v`
Expected: FAIL（`h.Rejoin` undefined）。

- [ ] **Step 3: 实现 `RoomHandler.Rejoin`**

在 `src/servers/roomsvr/internal/room_handler.go` 的 `Opengame`（行 49 `}`）之后追加：

```go
// Rejoin route="RoomHandler.rejoin"：重连改投 participant lobbyNode + 回当前拍卖快照。
func (h *RoomHandler) Rejoin(ctx context.Context, req *roompb.RPC_Rejoin_Req) (*roompb.RPC_Rejoin_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	gameID, uid, newLobby := req.GameId, req.Uid, req.NewLobbyNode
	h.rt.Submit(func() {
		code, hb, hbr, rem, itemID, currency := h.rt.Rejoin(gameID, uid, newLobby)
		replyProto(replier, &roompb.RPC_Rejoin_Rsp{
			Code: code, HighestBid: hb, HighestBidder: hbr,
			CountdownRemaining: rem, ItemId: itemID, Currency: currency,
		}, nil)
	})
	return nil, cluster.ErrDeferredReply
}
```

- [ ] **Step 4: 运行验证通过**

Run: `cd /game/GameServer && go test ./src/servers/roomsvr/internal/ -run TestRoomHandler_Rejoin -race -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/roomsvr/internal/room_handler.go src/servers/roomsvr/internal/room_handler_test.go
git commit -m "feat(room): RoomHandler.rejoin 薄壳（P4c）"
```

### Task C3: `Runtime.QueryGame` + `RoomHandler.Querygame`（只读判活）

**Files:**
- Modify: `src/servers/roomsvr/internal/runtime.go`（`Game()` 前追加 `QueryGame`）
- Modify: `src/servers/roomsvr/internal/room_handler.go`（`Rejoin` 后追加 `Querygame`）
- Test: `src/servers/roomsvr/internal/room_handler_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `src/servers/roomsvr/internal/room_handler_test.go`：

```go
func queryGameSync(t *testing.T, rt *Runtime, gameID string) *roompb.RPC_QueryGame_Rsp {
	t.Helper()
	r := newCapReplier()
	ctx := cluster.WithReplier(cluster.WithSession(context.Background(), &clusterpb.ClusterSession{}), r)
	h := NewRoomHandler(rt)
	if _, err := h.Querygame(ctx, &roompb.RPC_QueryGame_Req{GameId: gameID}); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	res := <-r.ch
	if res.err != nil {
		t.Fatalf("querygame err: %v", res.err)
	}
	var out roompb.RPC_QueryGame_Rsp
	if err := proto.Unmarshal(res.data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &out
}

func TestRoomHandler_QueryGame(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	defer rt.Stop()
	openGameSync(t, rt, &roompb.RPC_OpenGame_Req{
		GameId: "g1", ItemId: 7, CountdownSec: 30, Currency: "gold",
		Participants: []*roompb.Participant{{Uid: 1, LobbyNodeId: "1.2.1"}},
	})
	if q := queryGameSync(t, rt, "g1"); !q.Exists || q.Closed {
		t.Fatalf("open game should be exists&&!closed, got %+v", q)
	}
	if q := queryGameSync(t, rt, "nope"); q.Exists {
		t.Fatalf("absent game should be exists=false, got %+v", q)
	}
	runOnLoop(t, rt, func() { rt.Game("g1").closed = true })
	if q := queryGameSync(t, rt, "g1"); !q.Exists || !q.Closed {
		t.Fatalf("closed game should be exists&&closed, got %+v", q)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd /game/GameServer && go test ./src/servers/roomsvr/internal/ -run TestRoomHandler_QueryGame -v`
Expected: FAIL（`h.Querygame` undefined）。

- [ ] **Step 3a: 实现 `Runtime.QueryGame`**

在 `src/servers/roomsvr/internal/runtime.go` 的 `Rejoin` 之后（`Game()` 之前）插入：

```go
// QueryGame 主循环内只读判活：返回 (exists, closed)。
func (rt *Runtime) QueryGame(gameID string) (bool, bool) {
	g := rt.games[gameID]
	if g == nil {
		return false, false
	}
	return true, g.closed
}
```

- [ ] **Step 3b: 实现 `RoomHandler.Querygame`**

在 `src/servers/roomsvr/internal/room_handler.go` 的 `Rejoin` 之后追加：

```go
// Querygame route="RoomHandler.querygame"：只读判活（惰性 room-death 探测）。
func (h *RoomHandler) Querygame(ctx context.Context, req *roompb.RPC_QueryGame_Req) (*roompb.RPC_QueryGame_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	gameID := req.GameId
	h.rt.Submit(func() {
		exists, closed := h.rt.QueryGame(gameID)
		replyProto(replier, &roompb.RPC_QueryGame_Rsp{Exists: exists, Closed: closed}, nil)
	})
	return nil, cluster.ErrDeferredReply
}
```

- [ ] **Step 4: 运行验证通过**

Run: `cd /game/GameServer && go test ./src/servers/roomsvr/internal/ -race -v`
Expected: PASS（roomsvr 整包绿，含既有 bid/settle/opengame）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/roomsvr/internal/runtime.go src/servers/roomsvr/internal/room_handler.go src/servers/roomsvr/internal/room_handler_test.go
git commit -m "feat(room): RoomHandler.querygame 只读判活（惰性 room-death 探测，P4c）"
```

---

## Stage D — lobby：SC_ReconnectAuction 推送

### Task D1: `pushReconnectAuction` + `PushReconnectAuction`

**Files:**
- Modify: `src/servers/lobbysvr/internal/presence.go:24`（msgID 常量块）+ 文件末尾追加 push 函数
- Modify: `src/servers/lobbysvr/internal/runtime.go`（在 `PushMatchTimeout` `runtime.go:564` 后追加 wrapper）
- Test: `src/servers/lobbysvr/internal/presence_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `src/servers/lobbysvr/internal/presence_test.go`：

```go
func TestPushReconnectAuction(t *testing.T) {
	fp := newFakePresence()
	fp.online[10001] = "1.1.1"
	pushReconnectAuction(fp, 10001, "g1", 120, 7, 25, 9, "gold", 0)
	pushes := fp.Pushes()
	if len(pushes) != 1 || pushes[0].msgID != msgIDSCReconnectAuction || pushes[0].uid != 10001 {
		t.Fatalf("want one SC_ReconnectAuction push to 10001, got %+v", pushes)
	}
	var sc lobbypb.SC_ReconnectAuction
	if err := proto.Unmarshal(pushes[0].body, &sc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sc.GameId != "g1" || sc.HighestBid != 120 || sc.ItemId != 9 || sc.Currency != "gold" || sc.Status != 0 {
		t.Fatalf("reconnect snapshot body mismatch: %+v", &sc)
	}
	// 离线玩家：不推
	pushReconnectAuction(fp, 20002, "g2", 0, 0, 0, 0, "", 1)
	if len(fp.Pushes()) != 1 {
		t.Fatalf("offline player should not be pushed")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd /game/GameServer && go test ./src/servers/lobbysvr/internal/ -run TestPushReconnectAuction -v`
Expected: FAIL（`pushReconnectAuction`/`msgIDSCReconnectAuction` undefined）。

- [ ] **Step 3a: 加 msgID 常量**

在 `src/servers/lobbysvr/internal/presence.go:24`（`msgIDSCMatchTimeout uint32 = 2041` 行后）追加：

```go
	msgIDSCReconnectAuction uint32 = 2042
```

- [ ] **Step 3b: 加 push 函数（文件末尾）**

在 `src/servers/lobbysvr/internal/presence.go` 末尾追加：

```go
// pushReconnectAuction 若玩家在线，推 SC_ReconnectAuction（同步，off-loop 调用）。
func pushReconnectAuction(pc presenceClient, uid int64, gameID string, hb, hbr int64, rem int32, itemID int32, currency string, status int32) {
	gw, online := pc.Query(uid)
	if !online {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_ReconnectAuction{
		GameId: gameID, HighestBid: hb, HighestBidder: hbr, CountdownRemaining: rem,
		ItemId: itemID, Currency: currency, Status: status,
	})
	if err != nil {
		logger.Warn("push reconnect auction: marshal failed", logger.Int64("uid", uid), logger.Err(err))
		return
	}
	pc.Push(gw, uid, msgIDSCReconnectAuction, body)
}
```

- [ ] **Step 3c: 加 Runtime wrapper**

在 `src/servers/lobbysvr/internal/runtime.go` 的 `PushMatchTimeout`（`runtime.go:558-564`）之后追加：

```go
// PushReconnectAuction 若在线推 SC_ReconnectAuction（off-loop）。status：0=active，1=voided。
func (rt *Runtime) PushReconnectAuction(uid int64, gameID string, hb, hbr int64, rem int32, itemID int32, currency string, status int32) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go pushReconnectAuction(pc, uid, gameID, hb, hbr, rem, itemID, currency, status)
}
```

- [ ] **Step 4: 运行验证通过**

Run: `cd /game/GameServer && go test ./src/servers/lobbysvr/internal/ -run TestPushReconnectAuction -race -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/presence.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/presence_test.go
git commit -m "feat(lobby): SC_ReconnectAuction 推送（P4c）"
```

---

## Stage E — lobby：重连接回编排

### Task E1: reconnect hooks + ViaRouter + `tryReconnect` + Login 接线（alive 接回）

**Files:**
- Modify: `src/servers/lobbysvr/internal/runtime.go`（struct 字段、`NewRuntime` 默认、`Login` 接线、新增 `tryReconnect`/`rejoinResult`/三个 ViaRouter）
- Test: `src/servers/lobbysvr/internal/runtime_test.go`

- [ ] **Step 1: 写失败测试（重连 alive → 重建亲和 + 推 active 快照）**

追加到 `src/servers/lobbysvr/internal/runtime_test.go`：

```go
func TestReconnect_AliveRejoin(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fp := newFakePresence()
	fp.online[10001] = "1.1.1"
	runOnLoop(t, rt, func() {
		rt.presence = fp
		rt.queryOnline = func(uid int64, done func(string, string)) { done("1.7.1", "g1") }
		rt.rejoinRoom = func(uid int64, room, game, newLobby string, done func(rejoinResult)) {
			if room != "1.7.1" || game != "g1" || newLobby != rt.nodeID {
				t.Errorf("rejoin args mismatch: room=%s game=%s newLobby=%s", room, game, newLobby)
			}
			done(rejoinResult{code: 0, hb: 120, hbr: 10001, rem: 25, itemID: 9, currency: "gold"})
		}
	})
	loadPlayerSync(t, rt, 10001) // 触发 Login 重连分支
	runOnLoop(t, rt, func() {
		aff := rt.players[10001].RoomAffinity()
		if aff == nil || aff.roomNodeID != "1.7.1" || aff.gameID != "g1" || aff.currency != "gold" {
			t.Fatalf("reconnect should rebuild affinity, got %+v", aff)
		}
	})
	waitFor(t, func() bool { return fp.LastPushMsgID() == msgIDSCReconnectAuction })
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd /game/GameServer && go test ./src/servers/lobbysvr/internal/ -run TestReconnect_AliveRejoin -v`
Expected: FAIL（`rt.queryOnline`/`rt.rejoinRoom`/`rejoinResult` undefined）。

- [ ] **Step 3a: 加 struct 字段 + rejoinResult**

在 `src/servers/lobbysvr/internal/runtime.go` 的 `Runtime` 结构体末尾（`forwardBid` 字段 `runtime.go:75` 后）追加：

```go
	// 重连接回 hook（默认接真实 router；测试可替换为 fake）
	queryOnline func(uid int64, done func(roomNodeID, gameID string))
	rejoinRoom  func(uid int64, roomNodeID, gameID, newLobbyNode string, done func(rejoinResult))
	queryGame   func(roomNodeID, gameID string, done func(alive bool))
```

并在 `forwardBid` hook 定义附近（如 `RuntimeConfig` 上方）加类型：

```go
// rejoinResult RoomHandler.rejoin 的回包载荷（hook 层解耦 proto，便于单测）。
// code：0=接回（room 存在且未封盘）；非 0=已封盘/局不存在/room 不可达 → 作废。
type rejoinResult struct {
	code     int32
	hb, hbr  int64
	rem      int32
	itemID   int32
	currency string
}
```

- [ ] **Step 3b: `NewRuntime` 默认接真实实现**

在 `src/servers/lobbysvr/internal/runtime.go:107-113` 的 `if cfg.Cluster != nil {` 块内追加：

```go
		rt.queryOnline = rt.queryOnlineViaRouter
		rt.rejoinRoom = rt.rejoinRoomViaRouter
		rt.queryGame = rt.queryGameViaRouter
```

- [ ] **Step 3c: 加 `tryReconnect` + 三个 ViaRouter**

在 `src/servers/lobbysvr/internal/runtime.go` 的 `Settle`（`runtime.go:618`）之前插入：

```go
// tryReconnect 重连接回：off-loop 查 online room 绑定；有绑定则 rejoin room（改投+取快照），
// 据返回在主循环重建亲和 + 推 SC_ReconnectAuction(active)，room 死/已封盘则作废(voided)。
func (rt *Runtime) tryReconnect(uid int64) {
	if rt.queryOnline == nil || rt.rejoinRoom == nil {
		return
	}
	newLobby := rt.nodeID
	rt.queryOnline(uid, func(roomNodeID, gameID string) {
		if roomNodeID == "" || gameID == "" {
			return // 无对局绑定：正常登录
		}
		rt.rejoinRoom(uid, roomNodeID, gameID, newLobby, func(res rejoinResult) {
			rt.Submit(func() {
				p := rt.players[uid]
				if p == nil {
					return // 重连后又断连
				}
				if res.code == 0 { // 接回
					p.SetRoomAffinity(roomNodeID, gameID, res.currency)
					rt.PushReconnectAuction(uid, gameID, res.hb, res.hbr, res.rem, res.itemID, res.currency, 0)
				} else { // room 死/已封盘 → 作废
					rt.unbindRoom(uid)
					rt.PushReconnectAuction(uid, gameID, 0, 0, 0, 0, "", 1)
				}
			})
		})
	})
}

func (rt *Runtime) queryOnlineViaRouter(uid int64, done func(roomNodeID, gameID string)) {
	if rt.cls == nil {
		done("", "")
		return
	}
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		rsp, err := routerclient.CallViaSync[*onlinepb.RPC_Query_Rsp](
			ctx, cls, "onlinesvr", routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: uid})
		if err != nil || !rsp.Online || rsp.Entry == nil {
			done("", "")
			return
		}
		done(rsp.Entry.RoomNodeId, rsp.Entry.GameId)
	}()
}

func (rt *Runtime) rejoinRoomViaRouter(uid int64, roomNodeID, gameID, newLobbyNode string, done func(rejoinResult)) {
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		rsp, err := routerclient.CallViaSync[*roompb.RPC_Rejoin_Rsp](
			ctx, cls, "roomsvr", routerpb.RoutingMode_ROUTING_DIRECT, roomNodeID, "RoomHandler.rejoin",
			&roompb.RPC_Rejoin_Req{Uid: uid, GameId: gameID, NewLobbyNode: newLobbyNode})
		if err != nil {
			logger.Warn("reconnect: rejoin room unreachable, voiding", logger.Int64("uid", uid), logger.Err(err))
			done(rejoinResult{code: 2}) // room 不可达 → 作废
			return
		}
		done(rejoinResult{code: rsp.Code, hb: rsp.HighestBid, hbr: rsp.HighestBidder, rem: rsp.CountdownRemaining, itemID: rsp.ItemId, currency: rsp.Currency})
	}()
}

func (rt *Runtime) queryGameViaRouter(roomNodeID, gameID string, done func(alive bool)) {
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		rsp, err := routerclient.CallViaSync[*roompb.RPC_QueryGame_Rsp](
			ctx, cls, "roomsvr", routerpb.RoutingMode_ROUTING_DIRECT, roomNodeID, "RoomHandler.querygame",
			&roompb.RPC_QueryGame_Req{GameId: gameID})
		if err != nil {
			done(false) // room 不可达 → 视为死
			return
		}
		done(rsp.Exists && !rsp.Closed)
	}()
}
```

- [ ] **Step 3d: Login 接线**

在 `src/servers/lobbysvr/internal/runtime.go:194`（`reply(&lobbypb.RPC_Login_Rsp{...}, nil)` 行）之后插入一行：

```go
				rt.tryReconnect(uid) // 重连接回：查 online room 绑定 → rejoin 改投+快照 / 作废
```

（即位于 `reply(...)` 与 `rt.scanFriendAccepts(...)` 之间。）

- [ ] **Step 4: 运行验证通过 + 既有 Login/Disconnect 不回归**

Run: `cd /game/GameServer && go test ./src/servers/lobbysvr/internal/ -run 'TestReconnect_AliveRejoin|TestRuntime_Login|TestLobbyHandler' -race -v`
Expected: PASS（新用例 + 既有登录/handler 测试全绿；newTestRuntime 不设 queryOnline ⇒ tryReconnect 早退，既有测试不触发重连）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/runtime_test.go
git commit -m "feat(lobby): 重连接回编排 tryReconnect + rejoin/queryOnline/queryGame hook（P4c）"
```

### Task E2: 重连 void 分支（room 死/已封盘 → 作废）

**Files:**
- Test: `src/servers/lobbysvr/internal/runtime_test.go`（仅加测试，实现已在 E1）

- [ ] **Step 1: 写测试**

追加到 `src/servers/lobbysvr/internal/runtime_test.go`：

```go
func TestReconnect_VoidWhenRoomDead(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fp := newFakePresence()
	fp.online[10001] = "1.1.1"
	var unbound atomic.Int64
	runOnLoop(t, rt, func() {
		rt.presence = fp
		rt.unbindRoomFn = func(int64) { unbound.Add(1) }
		rt.queryOnline = func(uid int64, done func(string, string)) { done("1.7.1", "g1") }
		rt.rejoinRoom = func(uid int64, room, game, newLobby string, done func(rejoinResult)) {
			done(rejoinResult{code: 2}) // room 不可达/局不存在
		}
	})
	loadPlayerSync(t, rt, 10001)
	runOnLoop(t, rt, func() {
		if rt.players[10001].RoomAffinity() != nil {
			t.Fatalf("dead room reconnect must NOT set affinity")
		}
	})
	if unbound.Load() != 1 {
		t.Fatalf("void should unbindRoom once, got %d", unbound.Load())
	}
	waitFor(t, func() bool { return fp.LastPushMsgID() == msgIDSCReconnectAuction })
}
```

- [ ] **Step 2: 运行验证通过**

Run: `cd /game/GameServer && go test ./src/servers/lobbysvr/internal/ -run TestReconnect_VoidWhenRoomDead -race -v`
Expected: PASS。

- [ ] **Step 3: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime_test.go
git commit -m "test(lobby): 重连 room 死作废分支（P4c）"
```

### Task E3: 重连无绑定（正常登录，不接回）

**Files:**
- Test: `src/servers/lobbysvr/internal/runtime_test.go`

- [ ] **Step 1: 写测试**

追加到 `src/servers/lobbysvr/internal/runtime_test.go`：

```go
func TestReconnect_NoBindingNormalLogin(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	var rejoinCalled atomic.Int64
	runOnLoop(t, rt, func() {
		rt.queryOnline = func(uid int64, done func(string, string)) { done("", "") } // 无绑定
		rt.rejoinRoom = func(uid int64, room, game, newLobby string, done func(rejoinResult)) {
			rejoinCalled.Add(1)
			done(rejoinResult{code: 0})
		}
	})
	loadPlayerSync(t, rt, 10001)
	runOnLoop(t, rt, func() {
		if rt.players[10001].RoomAffinity() != nil {
			t.Fatalf("no-binding login must not set affinity")
		}
	})
	if rejoinCalled.Load() != 0 {
		t.Fatalf("no binding ⇒ rejoin must not be called, got %d", rejoinCalled.Load())
	}
}
```

- [ ] **Step 2: 运行验证通过**

Run: `cd /game/GameServer && go test ./src/servers/lobbysvr/internal/ -run TestReconnect_NoBindingNormalLogin -race -v`
Expected: PASS。

- [ ] **Step 3: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime_test.go
git commit -m "test(lobby): 重连无绑定走正常登录（P4c）"
```

---

## Stage F — lobby：Disconnect in-game 保留在线条目

### Task F1: in-game 跳过 onlineUnregister，非 in-game 维持注销

**Files:**
- Modify: `src/servers/lobbysvr/internal/runtime.go:206-218`
- Test: `src/servers/lobbysvr/internal/runtime_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `src/servers/lobbysvr/internal/runtime_test.go`：

```go
func TestDisconnect_InGamePreservesOnlineEntry(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	var unregistered atomic.Int64
	runOnLoop(t, rt, func() { rt.onlineUnregister = func(int64) { unregistered.Add(1) } })
	loadPlayerSync(t, rt, 10001)
	// in-game：置亲和后断连 → 不应注销在线条目（靠 TTL 过期作宽限窗）
	runOnLoop(t, rt, func() { rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold") })
	disconnectSync(t, rt, 10001)
	runOnLoop(t, rt, func() {
		if rt.players[10001] != nil {
			t.Fatalf("disconnect should evict in-memory player")
		}
	})
	if unregistered.Load() != 0 {
		t.Fatalf("in-game disconnect must NOT unregister online entry, got %d", unregistered.Load())
	}
	// 非 in-game：断连 → 立即注销
	loadPlayerSync(t, rt, 20002)
	disconnectSync(t, rt, 20002)
	if unregistered.Load() != 1 {
		t.Fatalf("non-in-game disconnect must unregister once, got %d", unregistered.Load())
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd /game/GameServer && go test ./src/servers/lobbysvr/internal/ -run TestDisconnect_InGamePreservesOnlineEntry -v`
Expected: FAIL（当前 Disconnect 无条件 onlineUnregister ⇒ in-game 计数为 1）。

- [ ] **Step 3: 实现——in-game 跳过注销**

将 `src/servers/lobbysvr/internal/runtime.go:206-218` 的 `Disconnect` 改为：

```go
// Disconnect 主循环内断连：flush 后剔除内存副本；非 in-game 立即注销在线，
// in-game（有 room 亲和）保留在线条目（含 room 绑定）靠 5min TTL 过期作重连宽限窗（P4c-2）。
func (rt *Runtime) Disconnect(uid int64) {
	p, ok := rt.players[uid]
	inGame := false
	if ok {
		inGame = p.RoomAffinity() != nil
		if rt.presence != nil {
			friends := p.Friend().List() // 主循环内取副本，off-loop goroutine 只触碰副本
			go fanoutPresence(rt.presence, uid, friends, false)
		}
		rt.flushPlayer(uid, p, func() { delete(rt.players, uid) })
	}
	if !inGame {
		// 非 in-game：无条件注销（迟到/重复断连仍幂等，best-effort）
		rt.onlineUnregister(uid)
	}
}
```

- [ ] **Step 4: 运行验证通过 + 既有 Disconnect 测试不回归**

Run: `cd /game/GameServer && go test ./src/servers/lobbysvr/internal/ -run 'TestDisconnect|TestRuntime_Login' -race -v`
Expected: PASS（既有断连测试不设亲和 ⇒ 仍走注销分支）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/runtime_test.go
git commit -m "feat(lobby): Disconnect 对 in-game 保留在线条目靠 TTL 过期（重连宽限窗，P4c）"
```

---

## Stage G — lobby：Purchase 惰性 room-death 探测

### Task G1: 大厅消费命中亲和时探活 room

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go:117-143`（`Purchase` 的亲和分支）
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `src/servers/lobbysvr/internal/lobby_handler_test.go`：

```go
func TestPurchase_ProbeRoomDeath(t *testing.T) {
	// room 仍活 → 维持禁购 code 2
	t.Run("alive keeps blocked", func(t *testing.T) {
		rt := newTestRuntime(t)
		defer rt.Stop()
		loadPlayerSync(t, rt, 10001)
		runOnLoop(t, rt, func() {
			rt.players[10001].Currency().Gain("seed", "gold", 500)
			rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
			rt.queryGame = func(room, game string, done func(bool)) { done(true) } // 仍活
		})
		if rsp := purchaseSync(t, rt, 10001, "op1", "gold", 10, 7); rsp.Code != 2 {
			t.Fatalf("alive room should keep purchase blocked (code 2), got %d", rsp.Code)
		}
		runOnLoop(t, rt, func() {
			if rt.players[10001].RoomAffinity() == nil {
				t.Fatalf("alive room must NOT clear affinity")
			}
		})
	})
	// room 死 → 清亲和 + unbind + code 3（请重试）
	t.Run("dead clears affinity", func(t *testing.T) {
		rt := newTestRuntime(t)
		defer rt.Stop()
		loadPlayerSync(t, rt, 10001)
		var unbound atomic.Int64
		runOnLoop(t, rt, func() {
			rt.players[10001].Currency().Gain("seed", "gold", 500)
			rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
			rt.unbindRoomFn = func(int64) { unbound.Add(1) }
			rt.queryGame = func(room, game string, done func(bool)) { done(false) } // 已死
		})
		if rsp := purchaseSync(t, rt, 10001, "op1", "gold", 10, 7); rsp.Code != 3 {
			t.Fatalf("dead room should return retry (code 3), got %d", rsp.Code)
		}
		runOnLoop(t, rt, func() {
			if rt.players[10001].RoomAffinity() != nil {
				t.Fatalf("dead room must clear affinity")
			}
		})
		if unbound.Load() != 1 {
			t.Fatalf("dead room should unbindRoom once, got %d", unbound.Load())
		}
	})
}
```

- [ ] **Step 2: 运行验证失败**

Run: `cd /game/GameServer && go test ./src/servers/lobbysvr/internal/ -run TestPurchase_ProbeRoomDeath -v`
Expected: FAIL（当前 Purchase 命中亲和直接 code 2；dead 子用例期望 code 3 失败）。

- [ ] **Step 3: 实现——亲和分支改惰性探活**

将 `src/servers/lobbysvr/internal/lobby_handler.go:123-126` 的：

```go
		if p.RoomAffinity() != nil {
			replyProto(replier, &lobbypb.SC_Purchase{Code: 2}, nil) // 2=对局中禁大厅消费（D6）
			return
		}
```

替换为：

```go
		if aff := p.RoomAffinity(); aff != nil {
			if h.rt.queryGame == nil { // 无探活 hook（部分单测）：维持禁购
				replyProto(replier, &lobbypb.SC_Purchase{Code: 2}, nil)
				return
			}
			roomNodeID, gameID := aff.roomNodeID, aff.gameID // 主循环内快照，off-loop 只读副本
			h.rt.queryGame(roomNodeID, gameID, func(alive bool) {
				if alive {
					replyProto(replier, &lobbypb.SC_Purchase{Code: 2}, nil) // 对局仍活：禁购（D6）
					return
				}
				h.rt.Submit(func() { // room 死：清亲和 + 清 online 绑定 + 令客户端重试
					if pp := h.rt.Player(uid); pp != nil {
						pp.ClearRoomAffinity()
					}
					h.rt.unbindRoom(uid)
					replyProto(replier, &lobbypb.SC_Purchase{Code: 3}, nil) // 3=对局已结束，请重试
				})
			})
			return
		}
```

- [ ] **Step 4: 运行验证通过 + 既有禁购/购买测试不回归**

Run: `cd /game/GameServer && go test ./src/servers/lobbysvr/internal/ -run 'TestPurchase|TestLobbyHandler_PurchaseRejectedDuringGame' -race -v`
Expected: PASS（既有 `TestLobbyHandler_PurchaseRejectedDuringGame` 不设 queryGame ⇒ 走 nil 分支返回 code 2，仍绿）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobby): Purchase 惰性探活 room-death（命中亲和反查，P4c）"
```

---

## Stage H — 集成骨架 + 文档 + 全量 -race 终审

### Task H1: 重连端到端集成骨架（build-tag，沙箱仅编译验证）

**Files:**
- Create: `src/servers/lobbysvr/internal/reconnect_integration_test.go`

- [ ] **Step 1: 写集成骨架（t.Skip，实跑需 Docker）**

```go
//go:build integration

package internal

import "testing"

// TestReconnectRejoin_EndToEnd 验证掉线 5min 内重连接回原拍卖局（需容器 NATS+etcd+MongoDB）。
// 流程：登录 L1 → 匹配开局 room X → 出价 → 掉线（in-game 保留在线条目）→
//       重连选 L2 → Login 查 online 拿 {X,g1} → RoomHandler.rejoin 改投 L2 + 回快照 →
//       重建亲和 + 收 SC_ReconnectAuction → 继续出价 → 收后续广播与结算（落点 L2）。
// 沙箱无 Docker（umbrella D10）：仅编译验证，实跑留 Docker host。
func TestReconnectRejoin_EndToEnd(t *testing.T) {
	t.Skip("integration: requires NATS+etcd+MongoDB container (no Docker in sandbox)")
}

// TestReconnect_RoomDeadVoided 验证 room 死/超 5min 窗 → 作废清亲和回大厅（mailbox/offline 兜结算）。
func TestReconnect_RoomDeadVoided(t *testing.T) {
	t.Skip("integration: requires NATS+etcd+MongoDB container (no Docker in sandbox)")
}
```

- [ ] **Step 2: 编译验证（含 integration tag）**

Run: `cd /game/GameServer && go vet -tags integration ./src/servers/lobbysvr/...`
Expected: PASS（编译通过，t.Skip 不实跑）。

- [ ] **Step 3: Commit**

```bash
git add src/servers/lobbysvr/internal/reconnect_integration_test.go
git commit -m "test(lobby): P4c 重连集成骨架（build-tag，沙箱仅编译验证）"
```

### Task H2: 文档同步

**Files:**
- Modify: `architecture.md`、`cluster.md`、`development.md`（按 CLAUDE.md 维护约定）

- [ ] **Step 1: 更新文档**

按实际改动同步（每处一两句即可，与既有体例一致）：
- `architecture.md`：lobby 分发主干补「Login 重连分支：查 online room 绑定 → `RoomHandler.rejoin` 改投+取快照 → 重建亲和 / 作废」；roomsvr 补 `rejoin`/`querygame` 两个 server-server route；新增客户端 push `SC_ReconnectAuction`(2042)。
- `cluster.md`：寻址表补 `lobby→room rejoin/querygame`（DIRECT key=room_node_id）、`lobby→online query`（CONSISTENT_HASH，重连读绑定）；记 `Directory.Register` 保留 room 绑定 + `Disconnect` 对 in-game 保留条目靠 TTL 过期。
- `development.md`：lobby 客户端 msg_id 段更新到 **2042**（`SC_ReconnectAuction`）。

- [ ] **Step 2: 校验文档无断链 / msg_id 一致**

Run: `cd /game/GameServer && grep -rn "2042\|SC_ReconnectAuction\|RoomHandler.rejoin\|querygame" architecture.md cluster.md development.md`
Expected: 各处均有对应记录。

- [ ] **Step 3: Commit**

```bash
git add architecture.md cluster.md development.md
git commit -m "docs: P4c 同步重连接回/改投/room-death 探测（架构/集群/开发文档）"
```

### Task H3: 全量编译 + vet + -race 终审

**Files:** 无（验证 + 收口）

- [ ] **Step 1: 全量编译**

Run: `cd /game/GameServer && go build ./...`
Expected: PASS。

- [ ] **Step 2: vet（含 integration tag）**

Run: `cd /game/GameServer && go vet ./... && go vet -tags integration ./...`
Expected: PASS。

- [ ] **Step 3: 全量 -race 测试**

Run: `cd /game/GameServer && go test ./... -race`
Expected: 全 PASS（onlinesvr / roomsvr / lobbysvr 含全部 P4c 新用例 + 既有用例无回归）。

- [ ] **Step 4: 孤儿清理自检**

Run: `cd /game/GameServer && gofmt -l src/servers/ protocal/ | grep -v "/gen/" || echo "gofmt clean"`
Expected: `gofmt clean`（生成代码 `gen/` 不计）。若有手写文件未格式化 → `gofmt -w` 后 amend 对应 commit。

- [ ] **Step 5: 终审无新增 commit（验证性任务）**

若 Step 1-4 全绿则无改动；如有修复，并入最近相关 commit（`git commit --amend` 或新 fix commit）。

---

## Self-Review（写完计划后对照 spec）

**1. Spec 覆盖：**
- §2 宽限窗（Register 保留 / Disconnect in-game）→ Task B1 + F1 ✓
- §2 惰性 room-death 探测（重连 + 禁购）→ Task E1/E2（重连判活）+ G1（禁购探活）✓
- §2 rejoin 合并 RPC（改投+快照+currency+判活）→ Task A1（proto）+ C1/C2（room）+ E1（lobby 编排）✓
- §4.1 重连接回流程（Login 分支：replay→register→query→rejoin）→ Task E1 Step 3d ✓
- §4.2 改投在 room 主循环 → Task C1（`Rejoin` 经 `RoomHandler` Submit）✓
- §4.3 禁购探活 → Task G1 ✓
- §5.1 Register 保留 → B1；§5.2 Login/Disconnect/编排/探活/推送 → D1/E1/E2/E3/F1/G1；§5.3 room rejoin/querygame → C1/C2/C3 ✓
- §6.6 不变式延续（重连无新持久写、结算路径不变）→ 未触碰 Settle/replayOffline 持久路径 ✓
- §7 proto（room Rejoin/QueryGame、lobby SC_ReconnectAuction 2042）→ A1 ✓
- §9 已知边界（②环、presence 不一致、settle 抢跑、双载窄窗）→ spec 已记，本计划不引入新写路径，无新增任务 ✓
- §10 测试（onlinesvr Register / roomsvr rejoin+querygame / lobby 重连分支+Disconnect+探活+推送 / 集成骨架）→ B1/C/D/E/F/G/H1 ✓

**2. Placeholder 扫描：** 无 TBD/TODO；每个代码步含完整代码、每条命令含期望输出。✓

**3. 类型/签名一致性：**
- `rejoinResult{code, hb, hbr, rem, itemID, currency}` 在 E1 定义并在 `tryReconnect`/fake/ViaRouter 一致使用 ✓
- hook 签名：`queryOnline func(uid, done func(roomNodeID, gameID string))`、`rejoinRoom func(uid, room, game, newLobby string, done func(rejoinResult))`、`queryGame func(room, game string, done func(alive bool))` 在 struct/默认/fake/Purchase/tryReconnect 全一致 ✓
- proto Go 字段：`NewLobbyNode`/`CountdownRemaining`/`HighestBidder`/`ItemId`/`Currency`/`Status`/`Exists`/`Closed` 与生成约定一致 ✓
- `Runtime.Rejoin` 6 返回值（code,hb,hbr,rem,itemID,currency）在 room handler 与 lobby fake 顺序一致 ✓
- SC_Purchase code 3（请重试）新增值，不改 proto（int32），G1 测试与 handler 一致 ✓

---

## 执行顺序 / 依赖

```
A(proto+routes) → B(online Register) ┐
                  C(room rejoin/querygame) ┤
                  D(lobby push) ┘
                       ↓
              E(lobby 重连编排, 依赖 A/C/D)
                       ↓
              F(Disconnect in-game) · G(Purchase 探活, 依赖 A 的 queryGame proto + E 的 hook)
                       ↓
              H(集成骨架 + 文档 + 全量 -race)
```

核心/并发 Task（**B Register、E1 重连编排+Login 接线、F Disconnect 语义、G Purchase 探活**）走独立 spec+质量双评审 + 整支 `-race` opus 终审（P2/P3a/P3b/P4a/P4b 反复证明 verbatim 计划代码会偏离/不全，终审 + `-race` 必做）。

分支：spec（已提交 `docs/p4c-reconnect-rejoin` 分支 commit 539aca7）+ 本 impl-plan 合一个 docs PR；code 另开 `feat/p4c-reconnect-rejoin` PR。合 PR 前 `git fetch && git rebase origin/main`（执行期 main 会前进）。
