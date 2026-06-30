package internal

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	roompb "project/protocal/gen/room"
	routerpb "project/protocal/gen/router"
	"project/src/common/logger"
	"project/src/common/taskqueue"
	"project/src/common/timewheel"
	"project/src/framework/cluster"
	"project/src/framework/cluster/routerclient"
)

// RuntimeConfig roomsvr 主循环配置
type RuntimeConfig struct {
	NodeID    string
	QueueSize int
	Tick      time.Duration
	Cluster   cluster.Cluster
}

// Runtime roomsvr 帧驱动单主循环：串行承载多局拍卖（零锁）。
type Runtime struct {
	nodeID string
	tq     *taskqueue.Queue
	tw     *timewheel.TimeWheel
	games  map[string]*Game
	cls    cluster.Cluster

	tick     time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once

	inflight atomic.Int64

	// off-loop 编排 hook（默认接真实 router；测试可替换）
	broadcast    func(lobbyNode string, uid int64, gameID string, hb, hbr int64, remaining int32)
	notifySettle func(lobbyNode string, uid, winner, price int64, gameID string, itemID int32, currency string) error
}

// NewRuntime 构造 roomsvr 主循环运行时
func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if cfg.Tick <= 0 {
		cfg.Tick = 100 * time.Millisecond
	}
	rt := &Runtime{
		nodeID: cfg.NodeID,
		tq:     taskqueue.New(cfg.QueueSize),
		tw:     timewheel.New(cfg.Tick, 512),
		games:  make(map[string]*Game),
		cls:    cfg.Cluster,
		tick:   cfg.Tick,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	if cfg.Cluster != nil {
		rt.broadcast = rt.broadcastViaRouter
		rt.notifySettle = rt.settleViaRouter
	}
	return rt
}

// Submit 跨 goroutine 把 fn 投递到主循环串行执行。
func (rt *Runtime) Submit(fn func()) { rt.tq.Enqueue(fn) }

// NodeID 返回本 room 节点 ID（opengame 回带）。
func (rt *Runtime) NodeID() string { return rt.nodeID }

// Start 启动主循环 goroutine
func (rt *Runtime) Start() { go rt.loop() }

// Stop 停止主循环并等待退出（可重复调用，仅首次真正关闭）
func (rt *Runtime) Stop() {
	rt.stopOnce.Do(func() { close(rt.stopCh) })
	<-rt.doneCh
}

func (rt *Runtime) loop() {
	defer close(rt.doneCh)
	ticker := time.NewTicker(rt.tick)
	defer ticker.Stop()
	for {
		select {
		case <-rt.stopCh:
			rt.drain()
			return
		case fn := <-rt.tq.C():
			fn()
		case <-ticker.C:
			rt.tw.Advance() // tick 推进各局倒计时
		}
	}
}

const drainTimeout = 5 * time.Second

func (rt *Runtime) drain() {
	deadline := time.After(drainTimeout)
	for rt.inflight.Load() > 0 {
		select {
		case fn := <-rt.tq.C():
			fn()
		case <-deadline:
			logger.Warn("room drain timeout, abandoning in-flight", logger.Int64("inflight", rt.inflight.Load()))
			return
		}
	}
}

// OpenGame 主循环内建局（按 gameId 幂等：已存在则不覆盖）。
func (rt *Runtime) OpenGame(gameID string, itemID, countdownSec int32, currency string, parts []Participant) {
	if _, ok := rt.games[gameID]; ok {
		return
	}
	g := NewGame(gameID, itemID, countdownSec, currency, parts)
	g.deadline = time.Now().Add(time.Duration(countdownSec) * time.Second)
	rt.games[gameID] = g
	gid := gameID
	rt.tw.AfterFunc(time.Duration(countdownSec)*time.Second, func() { rt.settle(gid) })
	logger.Info("room game opened", logger.String("gameId", gameID), logger.Int("participants", len(parts)))
}

// Bid 主循环内出价：校验并更新最高价。返回 (code, highestBid, highestBidder)。
// code: 0=接受；1=已封盘；2=局不存在/非参与者；3=未严格高于当前最高价。
func (rt *Runtime) Bid(gameID string, uid, amount int64) (int32, int64, int64) {
	g := rt.games[gameID]
	if g == nil || !g.isParticipant(uid) {
		return 2, 0, 0
	}
	if g.closed {
		return 1, g.HighestBid, g.HighestBidder
	}
	if amount <= g.HighestBid {
		return 3, g.HighestBid, g.HighestBidder
	}
	g.HighestBid = amount
	g.HighestBidder = uid
	return 0, g.HighestBid, g.HighestBidder
}

// broadcastState 主循环内快照拍卖态，off-loop 向各 participant lobby 广播（best-effort）。
func (rt *Runtime) broadcastState(gameID string) {
	g := rt.games[gameID]
	if g == nil || rt.broadcast == nil {
		return
	}
	type tgt struct {
		uid   int64
		lobby string
	}
	targets := make([]tgt, 0, len(g.Participants))
	for _, p := range g.Participants {
		targets = append(targets, tgt{p.UID, p.LobbyNodeID})
	}
	hb, hbr, rem := g.HighestBid, g.HighestBidder, g.remaining()
	bs := rt.broadcast
	rt.inflight.Add(1)
	go func() {
		defer func() { rt.inflight.Add(-1); rt.Submit(func() {}) }()
		for _, t := range targets {
			bs(t.lobby, t.uid, gameID, hb, hbr, rem)
		}
	}()
}

func (rt *Runtime) broadcastViaRouter(lobbyNode string, uid int64, gameID string, hb, hbr int64, remaining int32) {
	node, err := cluster.ParseNodeID(lobbyNode)
	if err != nil {
		logger.Warn("room broadcast: bad lobby nodeID", logger.String("nodeID", lobbyNode))
		return
	}
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	if err := rt.cls.Cast(ctx, node, "LobbyHandler.auctionstate",
		&roompb.RPC_AuctionState_Notify{Uid: uid, GameId: gameID, HighestBid: hb, HighestBidder: hbr, CountdownRemaining: remaining}); err != nil {
		logger.Warn("room broadcast: cast failed", logger.Int64("uid", uid), logger.Err(err))
	}
}

const (
	settleRetries      = 3
	settleRetryBackoff = 200 * time.Millisecond
)

// settle 倒计时到点（主循环回调，在主循环执行）：封盘定赢家 + off-loop 回告各 participant（at-least-once）。
func (rt *Runtime) settle(gameID string) {
	g := rt.games[gameID]
	if g == nil || g.closed {
		return // 幂等：已封盘不重复
	}
	g.closed = true
	winner, price, itemID, currency := g.HighestBidder, g.HighestBid, g.ItemID, g.Currency
	parts := append([]Participant(nil), g.Participants...)
	ns := rt.notifySettle
	if ns == nil {
		return
	}
	rt.inflight.Add(1)
	go func() {
		defer func() { rt.inflight.Add(-1); rt.Submit(func() {}) }()
		for _, p := range parts {
			for attempt := 0; attempt < settleRetries; attempt++ {
				if err := ns(p.LobbyNodeID, p.UID, winner, price, gameID, itemID, currency); err == nil {
					break
				} else if attempt == settleRetries-1 {
					logger.Warn("room settle notify exhausted retries",
						logger.Int64("uid", p.UID), logger.String("gameId", gameID), logger.Err(err))
				} else {
					time.Sleep(settleRetryBackoff)
				}
			}
		}
	}()
}

func (rt *Runtime) settleViaRouter(lobbyNode string, uid, winner, price int64, gameID string, itemID int32, currency string) error {
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	rsp, err := routerclient.CallViaSync[*roompb.RPC_Settle_Rsp](
		ctx, rt.cls, "lobbysvr", routerpb.RoutingMode_ROUTING_DIRECT, lobbyNode, "LobbyHandler.settle",
		&roompb.RPC_Settle_Req{Uid: uid, GameId: gameID, Winner: winner, Price: price, ItemId: itemID, Currency: currency})
	if err != nil {
		return err
	}
	if rsp.Code != 0 {
		return fmt.Errorf("settle code=%d", rsp.Code)
	}
	return nil
}

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

// QueryGame 主循环内只读判活：返回 (exists, closed)。
func (rt *Runtime) QueryGame(gameID string) (bool, bool) {
	g := rt.games[gameID]
	if g == nil {
		return false, false
	}
	return true, g.closed
}

// Game 主循环内取局（不存在返回 nil）
func (rt *Runtime) Game(gameID string) *Game { return rt.games[gameID] }
