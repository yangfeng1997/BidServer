package internal

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	matchpb "project/protocal/gen/match"
	roompb "project/protocal/gen/room"
	routerpb "project/protocal/gen/router"
	"project/src/common/logger"
	"project/src/common/taskqueue"
	"project/src/common/timewheel"
	"project/src/framework/cluster"
	"project/src/framework/cluster/routerclient"
)

const (
	defaultMatchSize    = 2
	defaultMMRWindow    = 200
	defaultItemID       = 1
	defaultCountdownSec = 30
	defaultCurrency     = "gold" // MVP 默认竞拍币种
)

// RuntimeConfig matchsvr 主循环配置
type RuntimeConfig struct {
	NodeID    string
	Cluster   cluster.Cluster
	MatchSize int
	MMRWindow int64
	QueueSize int
	Tick      time.Duration
	MaxWait   time.Duration // 玩家最长等待时间，超时后 reap 并回告 lobby（默认 30s）
}

// Runtime matchsvr 单主循环：串行承载 MMR 队列 + 凑桌（零锁）。
// off-loop 编排（开局/回告）经 go func 发起，回调经 Submit 回环；inflight 计停机 drain。
type Runtime struct {
	nodeID  string
	cls     cluster.Cluster
	tq      *taskqueue.Queue
	tw      *timewheel.TimeWheel
	queue   *matchQueue
	gameSeq int64

	tick     time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
	inflight atomic.Int64

	maxWait time.Duration

	// off-loop 编排 hook（默认接真实 router；测试可替换）
	openGame          func(gameID string, table []waiting) (roomNodeID string, err error)
	notifyGameStarted func(lobbyNode string, uid int64, gameID, roomNodeID, currency string) error
	notifyTimeout     func(lobbyNode string, uid int64, reqID string) error // 等待超时回告（best-effort, off-loop）
}

// NewRuntime 构造 matchsvr 主循环运行时
func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.MatchSize <= 0 {
		cfg.MatchSize = defaultMatchSize
	}
	if cfg.MMRWindow <= 0 {
		cfg.MMRWindow = defaultMMRWindow
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if cfg.Tick <= 0 {
		cfg.Tick = 100 * time.Millisecond
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = 30 * time.Second
	}
	rt := &Runtime{
		nodeID:  cfg.NodeID,
		cls:     cfg.Cluster,
		tq:      taskqueue.New(cfg.QueueSize),
		tw:      timewheel.New(cfg.Tick, 512),
		queue:   newMatchQueue(cfg.MatchSize, cfg.MMRWindow),
		tick:    cfg.Tick,
		maxWait: cfg.MaxWait,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	if cfg.Cluster != nil {
		rt.openGame = rt.openGameViaRouter
		rt.notifyGameStarted = rt.gameStartedViaRouter
		rt.notifyTimeout = rt.timeoutViaRouter
	}
	return rt
}

// Submit 跨 goroutine 把 fn 投递到主循环串行执行。
func (rt *Runtime) Submit(fn func()) { rt.tq.Enqueue(fn) }

// Start 启动主循环 goroutine
func (rt *Runtime) Start() {
	rt.tw.Tick(reapInterval, rt.reapExpired)
	go rt.loop()
}

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
			rt.tw.Advance() // 超时/窗口放宽 tick 留后续；P4a 骨架
		}
	}
}

const drainTimeout = 5 * time.Second

// drain 排空 tq 直到在途编排回调清零（或超时兜底）。
func (rt *Runtime) drain() {
	deadline := time.After(drainTimeout)
	for rt.inflight.Load() > 0 {
		select {
		case fn := <-rt.tq.C():
			fn()
		case <-deadline:
			logger.Warn("match drain timeout, abandoning in-flight", logger.Int64("inflight", rt.inflight.Load()))
			return
		}
	}
}

// OnRequest 主循环内处理一条匹配请求：去重入队 + 尝试凑桌。
func (rt *Runtime) OnRequest(req *matchpb.MatchRequest) {
	w := waiting{uid: req.Uid, reqID: req.ReqId, mmr: req.Mmr, lobbyNode: req.LobbyNodeId}
	if !rt.queue.Enqueue(w) {
		return // 重复请求（JetStream 重投），已 ack 即可
	}
	rt.tryFormTables()
}

// tryFormTables 主循环内反复凑桌直到无法再成桌，每桌交 off-loop 编排。
func (rt *Runtime) tryFormTables() {
	for {
		table, ok := rt.queue.FormTable()
		if !ok {
			return
		}
		rt.gameSeq++
		gameID := fmt.Sprintf("%s-%d", rt.nodeID, rt.gameSeq)
		rt.orchestrate(gameID, table)
	}
}

// orchestrate off-loop 编排开局 + 回告：⑥开局拿 room_node_id，⑦对每 participant 回告 lobby。
// 并发安全要点：
//   - 在主循环内（on-loop）捕获 og/ngs 快照，goroutine 不直接读 rt.openGame/rt.notifyGameStarted 字段。
//   - gameSeq 仅在主循环内自增，无并发写。
//   - 失败 Requeue 经 Submit 回主循环，队列只在主循环内被写。
//   - inflight 用 atomic 计数，跨 goroutine 安全。
func (rt *Runtime) orchestrate(gameID string, table []waiting) {
	og, ngs := rt.openGame, rt.notifyGameStarted
	if og == nil {
		return
	}
	rt.inflight.Add(1)
	go func() {
		defer func() {
			rt.inflight.Add(-1)
			rt.Submit(func() {}) // 唤醒 drain：成功路径本身不 Submit 任何东西，
			// 否则 Stop 时 drain 的 select 会空等到 drainTimeout（5s）才发现 inflight 已归零
		}()
		roomNodeID, err := og(gameID, table)
		if err != nil {
			logger.Warn("match open game failed",
				logger.String("gameId", gameID),
				logger.Int("participants", len(table)),
				logger.Err(err))
			rt.Submit(func() {
				for _, w := range table {
					rt.queue.Requeue(w)
				}
			})
			return
		}
		for _, w := range table {
			if ngs == nil {
				continue
			}
			if err := ngs(w.lobbyNode, w.uid, gameID, roomNodeID, defaultCurrency); err != nil {
				logger.Warn("match notify gamestarted failed",
					logger.Int64("uid", w.uid),
					logger.String("lobby", w.lobbyNode),
					logger.String("gameId", gameID),
					logger.Err(err))
			}
		}
		// 所有 GameStarted 回告已发完（持久落库后 ack 不变式满足）：清除 pending 守护，
		// lobby 侧 roomAffinity 已置，后续 lobby 侧即拒同 uid 再次入局（P4b-1）。
		rt.Submit(func() {
			for _, w := range table {
				rt.queue.clearPending(w.uid)
			}
		})
	}()
}

// queueLen 仅主循环/测试用：当前等待人数。
func (rt *Runtime) queueLen() int { return rt.queue.Len() }

// openGameViaRouter ⑥ 经 router CONSISTENT_HASH(gameId) 调 RoomHandler.opengame，拿回 room_node_id。
func (rt *Runtime) openGameViaRouter(gameID string, table []waiting) (string, error) {
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	parts := make([]*roompb.Participant, 0, len(table))
	for _, w := range table {
		parts = append(parts, &roompb.Participant{Uid: w.uid, LobbyNodeId: w.lobbyNode})
	}
	rsp, err := routerclient.CallViaSync[*roompb.RPC_OpenGame_Rsp](
		ctx, rt.cls, "roomsvr",
		routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, gameID,
		"RoomHandler.opengame",
		&roompb.RPC_OpenGame_Req{
			GameId:       gameID,
			ItemId:       defaultItemID,
			CountdownSec: defaultCountdownSec,
			Currency:     defaultCurrency,
			Participants: parts,
		},
	)
	if err != nil {
		return "", err
	}
	if rsp.Code != 0 {
		return "", fmt.Errorf("opengame code=%d", rsp.Code)
	}
	return rsp.RoomNodeId, nil
}

const reapInterval = 1 * time.Second

// reapExpired tw.Tick 回调（主循环 inline 执行，读 queue 安全）：移除超时等待者 + off-loop best-effort 回告。
func (rt *Runtime) reapExpired() {
	expired := rt.queue.ReapExpired(time.Now(), rt.maxWait)
	if len(expired) == 0 || rt.notifyTimeout == nil {
		return
	}
	nt := rt.notifyTimeout
	rt.inflight.Add(1)
	go func() {
		defer func() { rt.inflight.Add(-1); rt.Submit(func() {}) }()
		for _, w := range expired {
			if err := nt(w.lobbyNode, w.uid, w.reqID); err != nil {
				logger.Warn("match timeout notify failed",
					logger.Int64("uid", w.uid),
					logger.Err(err))
			}
		}
	}()
}

// timeoutViaRouter 经 router DIRECT(lobbyNode) Cast LobbyHandler.matchtimeout 超时回告。
func (rt *Runtime) timeoutViaRouter(lobbyNode string, uid int64, reqID string) error {
	node, err := cluster.ParseNodeID(lobbyNode)
	if err != nil {
		return err
	}
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	return rt.cls.Cast(ctx, node, "LobbyHandler.matchtimeout", &matchpb.RPC_MatchTimeout_Notify{Uid: uid, ReqId: reqID})
}

// gameStartedViaRouter ⑦ 经 router DIRECT(lobbyNode) 调 LobbyHandler.gamestarted 回告。
func (rt *Runtime) gameStartedViaRouter(lobbyNode string, uid int64, gameID, roomNodeID, currency string) error {
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	rsp, err := routerclient.CallViaSync[*matchpb.RPC_GameStarted_Rsp](
		ctx, rt.cls, "lobbysvr",
		routerpb.RoutingMode_ROUTING_DIRECT, lobbyNode,
		"LobbyHandler.gamestarted",
		&matchpb.RPC_GameStarted_Req{Uid: uid, GameId: gameID, RoomNodeId: roomNodeID, Currency: currency},
	)
	if err != nil {
		return err
	}
	if rsp.Code != 0 {
		return fmt.Errorf("gamestarted code=%d", rsp.Code)
	}
	return nil
}
