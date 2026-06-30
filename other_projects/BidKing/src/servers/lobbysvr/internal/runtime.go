package internal

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
	lobbypb "project/protocal/gen/lobby"
	matchpb "project/protocal/gen/match"
	onlinepb "project/protocal/gen/online"
	roompb "project/protocal/gen/room"
	routerpb "project/protocal/gen/router"
	"project/src/common/logger"
	"project/src/common/taskqueue"
	"project/src/common/timewheel"
	"project/src/framework/cluster"
	"project/src/framework/cluster/routerclient"
)

const mailListLimit = 50

// rejoinResult RoomHandler.rejoin 的回包载荷（hook 层解耦 proto，便于单测）。
// code：0=接回（room 存在且未封盘）；非 0=已封盘/局不存在/room 不可达 → 作废。
type rejoinResult struct {
	code     int32
	hb, hbr  int64
	rem      int32
	itemID   int32
	currency string
}

// RuntimeConfig 主循环运行时配置
type RuntimeConfig struct {
	NodeID        string
	Cluster       cluster.Cluster
	Store         DocStore
	MailStore     MailStore
	OfflineStore  OfflineStore
	QueueSize     int
	Tick          time.Duration
	FlushInterval time.Duration
}

// Runtime lobby 单主循环运行时：串行承载全部玩家 EC 逻辑（零锁）
type Runtime struct {
	nodeID     string
	cls        cluster.Cluster
	store      DocStore
	mailStore  MailStore
	events     *Events
	tq         *taskqueue.Queue
	tw         *timewheel.TimeWheel
	players    map[int64]*Player
	dirtyFlush map[int64]bool // 待合并 flush 的玩家集合（coalesceFlush 消费）

	tick          time.Duration
	flushInterval time.Duration
	stopCh        chan struct{}
	doneCh        chan struct{}
	stopOnce      sync.Once
	inflight      atomic.Int64 // 在途 flush 回调计数（停机 drain 等其归零）

	// 在线注册/注销/活跃刷新（默认接真实 router；测试可替换为桩）
	onlineRegister   func(uid int64, gatewayNodeID string)
	onlineUnregister func(uid int64)
	onlineTouch      func(uid int64)

	// presence 在线查询 + 客户端推送（默认接真实 router/gate；测试可替换为 fake；nil 时跳过 fan-out）
	presence presenceClient

	reqSeq       int64                                 // 匹配请求序号（主循环内自增，拼 reqId）
	publishMatch func(req *matchpb.MatchRequest) error // 发布匹配请求（默认经 router；测试可替换）

	// bindRoom 同步 online room 绑定（默认经 router；测试可替换为桩）
	bindRoom func(uid int64, roomNodeID, gameID string)

	offlineStore OfflineStore
	// unbindRoomFn 清 online room 绑定（结算/作废调用；测试可替换为桩）
	unbindRoomFn func(uid int64)
	// forwardBid 转发出价到 room（off-loop；测试可替换为桩）
	forwardBid func(uid int64, roomNodeID, gameID string, amount int64, done func(code int32, highest int64))

	// 重连接回 hook（默认接真实 router；测试可替换为 fake）
	queryOnline func(uid int64, done func(roomNodeID, gameID string))
	rejoinRoom  func(uid int64, roomNodeID, gameID, newLobbyNode string, done func(rejoinResult))
	queryGame   func(roomNodeID, gameID string, done func(alive bool))
}

func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if cfg.Tick <= 0 {
		cfg.Tick = 100 * time.Millisecond
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 30 * time.Second
	}
	rt := &Runtime{
		nodeID:        cfg.NodeID,
		cls:           cfg.Cluster,
		store:         cfg.Store,
		mailStore:     cfg.MailStore,
		events:        NewEvents(),
		tq:            taskqueue.New(cfg.QueueSize),
		tw:            timewheel.New(cfg.Tick, 512),
		players:       make(map[int64]*Player),
		dirtyFlush:    make(map[int64]bool),
		tick:          cfg.Tick,
		flushInterval: cfg.FlushInterval,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	rt.onlineRegister = rt.registerOnline
	rt.onlineUnregister = rt.unregisterOnline
	rt.onlineTouch = rt.touchOnline
	rt.offlineStore = cfg.OfflineStore
	if cfg.Cluster != nil {
		rt.presence = clusterPresence{cls: cfg.Cluster}
		rt.publishMatch = rt.publishMatchViaRouter
		rt.bindRoom = rt.bindRoomViaRouter
		rt.unbindRoomFn = rt.unbindRoomViaRouter
		rt.forwardBid = rt.forwardBidViaRouter
		rt.queryOnline = rt.queryOnlineViaRouter
		rt.rejoinRoom = rt.rejoinRoomViaRouter
		rt.queryGame = rt.queryGameViaRouter
	}
	return rt
}

// Submit 跨 goroutine 把 fn 投递到主循环串行执行。
// Stop 之后投递的任务不再执行（队列不再被消费）。
func (rt *Runtime) Submit(fn func()) { rt.tq.Enqueue(fn) }

// Start 注册周期 flush 并启动主循环 goroutine
func (rt *Runtime) Start() {
	rt.events.CurrencyChanged.Subscribe(func(e CurrencyChanged) {
		logger.Info("currency changed",
			logger.Int64("uid", e.UID), logger.String("kind", e.Kind), logger.Int64("delta", e.Delta))
	})
	rt.tw.Tick(rt.flushInterval, rt.flushAllDirty)
	rt.tw.Tick(coalesceFlushInterval, rt.coalesceFlush)
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
			rt.flushAllDirty()
			rt.drain()
			return
		case fn := <-rt.tq.C():
			fn()
		case <-ticker.C:
			rt.tw.Advance()
		}
	}
}

const drainTimeout = 5 * time.Second

// drain 排空 tq 直到在途 flush 回调清零（或超时兜底）。
func (rt *Runtime) drain() {
	deadline := time.After(drainTimeout)
	for rt.inflight.Load() > 0 {
		select {
		case fn := <-rt.tq.C():
			fn()
		case <-deadline:
			logger.Warn("lobby drain timeout, abandoning in-flight",
				logger.Int64("inflight", rt.inflight.Load()))
			return
		}
	}
}

// Login 主循环内登录：加载/建档玩家工作副本 + 在线注册 + reply（异步回包由调用方经 Replier 发出）
func (rt *Runtime) Login(uid int64, gatewayNodeID string, reply func(*lobbypb.RPC_Login_Rsp, error)) {
	if _, ok := rt.players[uid]; ok {
		rt.onlineRegister(uid, gatewayNodeID)
		reply(&lobbypb.RPC_Login_Rsp{Code: 0, Uid: uid, LobbyNodeId: rt.nodeID}, nil)
		return
	}
	rt.store.Load(rt.tq, uid, func(doc *PlayerDoc, found bool, err error) {
		if err != nil {
			reply(nil, err)
			return
		}
		if !found || doc == nil {
			doc = NewPlayerDoc(uid)
		}
		rt.players[uid] = buildPlayer(uid, doc)
		rt.players[uid].attachMail(rt.mailStore)
		rt.events.PlayerLoaded.Publish(PlayerLoaded{UID: uid})
		p := rt.players[uid]
		rt.replayOffline(uid, p, func() { // 重放先于放行：维持 CanAfford⇒Spend必成
			rt.onlineRegister(uid, gatewayNodeID)
			reply(&lobbypb.RPC_Login_Rsp{Code: 0, Uid: uid, LobbyNodeId: rt.nodeID}, nil)
			rt.tryReconnect(uid) // 重连接回：查 online room 绑定 → rejoin 改投+快照 / 作废
			rt.scanFriendAccepts(uid, p, func() {
				if rt.presence != nil {
					friends := p.Friend().List() // 主循环内取副本，off-loop goroutine 只触碰副本
					go fanoutPresence(rt.presence, uid, friends, true)
				}
			})
		})
	})
}

// Disconnect 主循环内断连：flush 后剔除内存副本；非 in-game 立即注销在线，
// in-game（有 room 亲和）保留在线条目（含 room 绑定）靠 5min TTL 过期作重连宽限窗（P4c-2）。
func (rt *Runtime) Disconnect(uid int64) {
	p, ok := rt.players[uid]
	inGame := false
	if ok {
		inGame = p.RoomAffinity() != nil // 必须在 flushPlayer 剔除前读取：决定是否保留在线条目
		if rt.presence != nil {
			friends := p.Friend().List() // 主循环内取副本，off-loop goroutine 只触碰副本
			go fanoutPresence(rt.presence, uid, friends, false)
		}
		rt.flushPlayer(uid, p, func(ok bool) {
			if ok {
				delete(rt.players, uid) // 仅落库成功才剔除；失败保留待重连复用/coalesce/drain
			}
		})
	}
	if !inGame {
		// 非 in-game：无条件注销（迟到/重复断连仍幂等，best-effort）
		rt.onlineUnregister(uid)
	}
}

// Player 主循环内取玩家（不存在返回 nil）
func (rt *Runtime) Player(uid int64) *Player { return rt.players[uid] }

// NotifyNewMail 若收件人在线，off-loop 推 SC_MailNew（best-effort，不阻塞主循环）。
func (rt *Runtime) NotifyNewMail(to, from int64, mailType string) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go notifyNewMail(pc, to, from, mailType)
}

// flushAllDirty 周期 flush 全部玩家的脏组件
func (rt *Runtime) flushAllDirty() {
	for uid, p := range rt.players {
		rt.flushPlayer(uid, p, nil)
	}
}

// flushPlayer 异步落库脏组件：主循环内取全部脏组件快照 + 乐观清脏，
// 折成一个原子 FlushFields（单文档多字段 $set）；写失败则全批重标脏、下次重试。
// after(ok) 在该批写完成后调用，ok=true 表示落库成功，ok=false 表示失败；
// 各调用方自行决定是否依据 ok 做门控（Disconnect 仅 ok 时剔除；其余站点 B2/B3/B4 接真失败语义）。
// 全部回调均在主循环 goroutine 串行执行，故无需加锁。
func (rt *Runtime) flushPlayer(uid int64, p *Player, after func(ok bool)) {
	fields := make(map[string]any)
	var dirty []Component
	for _, c := range p.Components() {
		if !c.Dirty() {
			continue
		}
		fields[c.Field()] = c.Snapshot()
		c.ClearDirty()
		dirty = append(dirty, c)
	}
	if len(fields) == 0 { // 无脏：视为成功，无需落库
		if after != nil {
			after(true)
		}
		return
	}
	rt.inflight.Add(1)
	rt.store.FlushFields(rt.tq, uid, fields, func(err error) {
		defer rt.inflight.Add(-1)
		ok := err == nil
		if !ok {
			for _, c := range dirty {
				c.MarkDirty() // 整批失败：全部重标脏，下次重试
			}
			logger.Warn("lobby flush failed", logger.Int64("uid", uid), logger.Err(err))
		}
		if after != nil {
			after(ok)
		}
	})
}

// PublishCurrencyChanged 主循环内发布货币变动事件（副作用通知）
func (rt *Runtime) PublishCurrencyChanged(uid int64, kind string, delta int64) {
	rt.events.CurrencyChanged.Publish(CurrencyChanged{UID: uid, Kind: kind, Delta: delta})
}

const coalesceFlushInterval = 1 * time.Second

// FlushSoon 键点 flush：标记 uid 待 flush，由 coalesceFlush 合并落库（避免写放大）。
func (rt *Runtime) FlushSoon(uid int64) { rt.dirtyFlush[uid] = true }

// coalesceFlush 合并 flush 所有待 flush 玩家（coalesce tick 回调，主循环执行）。
func (rt *Runtime) coalesceFlush() {
	for uid := range rt.dirtyFlush {
		if p, ok := rt.players[uid]; ok {
			rt.flushPlayer(uid, p, nil)
		}
		delete(rt.dirtyFlush, uid)
	}
}

// grantAttachments 主循环内把附件发放进玩家组件，以 opID 持久幂等去重（重排后 mailID 为键）。
// 同一封邮件可能含多个同组件附件（如双币种/双道具），故按下标派生 opID:index 子键，
// 避免同组件第二个附件被去重环误判为重复而漏发；下标稳定 ⇒ 重放仍幂等。
func (rt *Runtime) grantAttachments(uid int64, p *Player, opID string, atts []Attachment) {
	for i, a := range atts {
		aOpID := opID
		if opID != "" {
			aOpID = opID + ":" + strconv.Itoa(i)
		}
		if a.Kind == "item" {
			p.Bag().Add(aOpID, int32(a.ID), int32(a.Count))
		} else {
			p.Currency().Gain(aOpID, a.Kind, a.Count)
			rt.PublishCurrencyChanged(uid, a.Kind, a.Count)
		}
	}
}

// scanFriendAccepts 扫描 to=uid 的未处理 friend_accept，逐条加好友 + claim；完成调 after（可 nil）。
func (rt *Runtime) scanFriendAccepts(uid int64, p *Player, after func()) {
	if rt.mailStore == nil { // 未接线 mailStore（如部分单测）→ 安全跳过
		if after != nil {
			after()
		}
		return
	}
	rt.mailStore.PendingFriendAccepts(rt.tq, uid, func(mails []MailDoc, err error) {
		if err != nil {
			logger.Warn("scan friend_accept failed", logger.Int64("uid", uid), logger.Err(err))
			if after != nil {
				after()
			}
			return
		}
		changed := false
		for _, m := range mails {
			if p.Friend().Add(m.From) {
				changed = true
			}
			rt.mailStore.Claim(rt.tq, m.ID, uid, func(bool, *MailDoc, error) {})
		}
		if changed {
			rt.FlushSoon(uid)
		}
		if after != nil {
			after()
		}
	})
}

// registerOnline 经 router 向 onlinesvr 注册在线（best-effort，off-loop goroutine，不阻塞主循环）
func (rt *Runtime) registerOnline(uid int64, gatewayNodeID string) {
	if rt.cls == nil {
		return
	}
	cls, nodeID := rt.cls, rt.nodeID
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		if _, err := routerclient.CallViaSync[*onlinepb.RPC_Register_Rsp](
			ctx, cls, "onlinesvr",
			routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.register",
			&onlinepb.RPC_Register_Req{Uid: uid, GatewayNodeId: gatewayNodeID, LobbyNodeId: nodeID},
		); err != nil {
			logger.Warn("lobby login: online register failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}

// unregisterOnline 经 router 向 onlinesvr 注销（best-effort，off-loop）
func (rt *Runtime) unregisterOnline(uid int64) {
	if rt.cls == nil {
		return
	}
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		if _, err := routerclient.CallViaSync[*onlinepb.RPC_Unregister_Rsp](
			ctx, cls, "onlinesvr",
			routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.unregister",
			&onlinepb.RPC_Unregister_Req{Uid: uid},
		); err != nil {
			logger.Warn("lobby disconnect: online unregister failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}

const touchThrottle = 2 * time.Minute

// Touch 主循环内刷新活跃：节流后 off-loop Touch onlinesvr。
func (rt *Runtime) Touch(uid int64) {
	p, ok := rt.players[uid]
	if !ok {
		return
	}
	now := time.Now().UnixNano()
	if p.lastTouch != 0 && now-p.lastTouch < int64(touchThrottle) {
		return
	}
	p.lastTouch = now
	rt.onlineTouch(uid)
}

// touchOnline 经 router 向 onlinesvr 刷新活跃（best-effort，off-loop）
func (rt *Runtime) touchOnline(uid int64) {
	if rt.cls == nil {
		return
	}
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		if _, err := routerclient.CallViaSync[*onlinepb.RPC_Touch_Rsp](
			ctx, cls, "onlinesvr",
			routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.touch",
			&onlinepb.RPC_Touch_Req{Uid: uid},
		); err != nil {
			logger.Warn("lobby touch: online touch failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}

// StartMatch 主循环内发起匹配：生成幂等 reqId，off-loop 经 router 发布到 JetStream（best-effort，不阻塞主循环）。
func (rt *Runtime) StartMatch(uid, mmr int64) {
	if rt.publishMatch == nil {
		return
	}
	rt.reqSeq++
	reqID := fmt.Sprintf("%s-%d-%d", rt.nodeID, uid, rt.reqSeq)
	req := &matchpb.MatchRequest{Uid: uid, ReqId: reqID, Mmr: mmr, LobbyNodeId: rt.nodeID}
	pub := rt.publishMatch
	go func() {
		if err := pub(req); err != nil {
			logger.Warn("lobby start match: publish failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}

// publishMatchViaRouter 经 router publishmatch 发布匹配请求（直接 CallAnySync 到任一 routersvr）。
func (rt *Runtime) publishMatchViaRouter(req *matchpb.MatchRequest) error {
	if rt.cls == nil {
		return nil
	}
	cls := rt.cls
	ctx := cluster.WithCluster(context.Background(), cls)
	data, err := cls.CallAnySync(ctx, "routersvr", "RouterHandler.publishmatch", req)
	if err != nil {
		return err
	}
	var rsp matchpb.RPC_PublishMatch_Rsp
	if err := proto.Unmarshal(data, &rsp); err != nil {
		return err
	}
	if rsp.Code != 0 {
		return fmt.Errorf("router publishmatch code=%d", rsp.Code)
	}
	return nil
}

// BindRoom 经注入的 hook 同步 online room 绑定（GameStarted 内调用）。
func (rt *Runtime) BindRoom(uid int64, roomNodeID, gameID string) {
	if rt.bindRoom != nil {
		rt.bindRoom(uid, roomNodeID, gameID)
	}
}

// bindRoomViaRouter 经 router CONSISTENT_HASH(uid) 调 OnlineHandler.bindroom 绝对写 room 绑定（best-effort，off-loop）。
func (rt *Runtime) bindRoomViaRouter(uid int64, roomNodeID, gameID string) {
	if rt.cls == nil {
		return
	}
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		if _, err := routerclient.CallViaSync[*onlinepb.RPC_BindRoom_Rsp](
			ctx, cls, "onlinesvr",
			routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.bindroom",
			&onlinepb.RPC_BindRoom_Req{Uid: uid, RoomNodeId: roomNodeID, GameId: gameID},
		); err != nil {
			logger.Warn("lobby gamestarted: bind room failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}

// PushMatchFound 若玩家在线，off-loop 推 SC_MatchFound（best-effort，不阻塞主循环）。
func (rt *Runtime) PushMatchFound(uid int64, roomNodeID, gameID string) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go pushMatchFound(pc, uid, roomNodeID, gameID)
}

// unbindRoom 经注入 hook 清 online room 绑定（结算/作废调用）。
func (rt *Runtime) unbindRoom(uid int64) {
	if rt.unbindRoomFn != nil {
		rt.unbindRoomFn(uid)
	}
}

func (rt *Runtime) unbindRoomViaRouter(uid int64) {
	if rt.cls == nil {
		return
	}
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		if _, err := routerclient.CallViaSync[*onlinepb.RPC_UnbindRoom_Rsp](
			ctx, cls, "onlinesvr", routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.unbindroom", &onlinepb.RPC_UnbindRoom_Req{Uid: uid}); err != nil {
			logger.Warn("settle: unbind room failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}

func (rt *Runtime) forwardBidViaRouter(uid int64, roomNodeID, gameID string, amount int64, done func(int32, int64)) {
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		rsp, err := routerclient.CallViaSync[*roompb.RPC_Bid_Rsp](
			ctx, cls, "roomsvr", routerpb.RoutingMode_ROUTING_DIRECT, roomNodeID, "RoomHandler.bid",
			&roompb.RPC_Bid_Req{GameId: gameID, Uid: uid, Amount: amount})
		if err != nil {
			logger.Warn("lobby bid: forward failed, voiding affinity", logger.Int64("uid", uid), logger.Err(err))
			rt.Submit(func() { // room 不可达 → 轻量作废（§8.3）
				if p := rt.players[uid]; p != nil {
					p.ClearRoomAffinity()
				}
				rt.unbindRoom(uid)
			})
			done(2, 0)
			return
		}
		done(rsp.Code, rsp.HighestBid)
	}()
}

// PushAuctionState 若在线推 SC_AuctionState（off-loop）
func (rt *Runtime) PushAuctionState(uid int64, gameID string, hb, hbr int64, rem int32) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go pushAuctionState(pc, uid, gameID, hb, hbr, rem)
}

// PushAuctionResult 若在线推 SC_AuctionResult（off-loop）
func (rt *Runtime) PushAuctionResult(uid int64, gameID string, winner, price int64, currency string, itemID int32) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go pushAuctionResult(pc, uid, gameID, winner, price, currency, itemID)
}

// PushMatchTimeout 若在线推 SC_MatchTimeout（off-loop）
func (rt *Runtime) PushMatchTimeout(uid int64) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go pushMatchTimeout(pc, uid)
}

// PushReconnectAuction 若在线推 SC_ReconnectAuction（off-loop）。status：0=active，1=voided。
func (rt *Runtime) PushReconnectAuction(uid int64, gameID string, hb, hbr int64, rem int32, itemID int32, currency string, status int32) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go pushReconnectAuction(pc, uid, gameID, hb, hbr, rem, itemID, currency, status)
}

// replayOffline 登录链：加载离线消息逐条重放（Spend+Add ops=opID），落库后 $pull，再调 after。
func (rt *Runtime) replayOffline(uid int64, p *Player, after func()) {
	if rt.offlineStore == nil {
		after()
		return
	}
	rt.offlineStore.Load(rt.tq, uid, func(msgs []OfflineMsg, err error) {
		if err != nil {
			logger.Warn("replay offline: load failed", logger.Int64("uid", uid), logger.Err(err))
			after()
			return
		}
		if len(msgs) == 0 {
			after()
			return
		}
		opIDs := make([]string, 0, len(msgs))
		for _, m := range msgs {
			// 保护该 opID 免于 FIFO 淘汰，直到 Ack($pull) 成功——防滞留消息再登 double-apply（⑤）。
			p.Currency().ProtectOp(m.OpID)
			p.Bag().ProtectOp(m.OpID)
			rt.applyOfflineMsg(uid, p, m)
			opIDs = append(opIDs, m.OpID)
		}
		rt.flushPlayer(uid, p, func(ok bool) {
			if ok { // 持久落库成功才 $pull（§6.6）
				rt.offlineStore.Ack(rt.tq, uid, opIDs, func(aerr error) {
					if aerr != nil {
						logger.Warn("replay offline: ack failed", logger.Int64("uid", uid), logger.Err(aerr))
						return // Ack 失败：保留保护，消息滞留，下次登录重放仍由持久环去重
					}
					// $pull 成功，消息不可能再重放，解保护使 opID 恢复可淘汰
					for _, id := range opIDs {
						p.Currency().UnprotectOp(id)
						p.Bag().UnprotectOp(id)
					}
				})
			}
			after() // 续登录：grants 内存可见，失败时消息留存下次登录重放（opID 幂等收敛）
		})
	})
}

// applyOfflineMsg 重放一条离线消息（持久 ops 去重，跨路/重投幂等）。
func (rt *Runtime) applyOfflineMsg(uid int64, p *Player, m OfflineMsg) {
	switch m.Type {
	case OfflineMsgSettle:
		if m.Price > 0 {
			if _, ok := p.Currency().Spend(m.OpID, m.Currency, m.Price); ok {
				rt.PublishCurrencyChanged(uid, m.Currency, -m.Price)
			} else {
				logger.Warn("replay offline settle: insufficient, item granted, charge waived",
					logger.Int64("uid", uid), logger.String("gameId", m.OpID))
			}
		}
		p.Bag().Add(m.OpID, m.ItemID, 1)
	default:
		logger.Warn("replay offline: unknown msg type", logger.Int64("uid", uid), logger.String("type", m.Type))
	}
}

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

// Settle 主循环内结算落地：清亲和 + 清 online 绑定 + 推结果；赢家在线扣发(持久 ops)、离线投 inbox。
// done(code)：0=已落地（持久落库后才 ack）；1=离线投递失败（room 重投）。§6.6 不变式。
func (rt *Runtime) Settle(uid int64, gameID string, winner, price int64, currency string, itemID int32, done func(int32)) {
	p := rt.players[uid]
	if p != nil {
		p.ClearRoomAffinity()
	}
	rt.unbindRoom(uid)
	rt.PushAuctionResult(uid, gameID, winner, price, currency, itemID)
	if uid != winner {
		done(0) // 输家无经济副作用
		return
	}
	if p != nil { // 在线赢家
		if price > 0 {
			if _, ok := p.Currency().Spend(gameID, currency, price); ok {
				rt.PublishCurrencyChanged(uid, currency, -price)
			} else {
				logger.Warn("settle online: insufficient, item granted, charge waived",
					logger.Int64("uid", uid), logger.String("gameId", gameID))
			}
		}
		p.Bag().Add(gameID, itemID, 1)
		rt.flushPlayer(uid, p, func(ok bool) {
			if ok {
				done(0) // 持久落库后才 ack（§6.6）
			} else {
				done(1) // 落库失败：令 room 重投（opID=gameId 持久幂等保恰一次）
			}
		})
		return
	}
	rt.offlinePush(uid, OfflineMsg{Type: OfflineMsgSettle, OpID: gameID, Price: price, Currency: currency, ItemID: itemID}, done)
}

// offlinePush off-loop 投递离线消息，push 成功才 ack(0)，失败 ack(1) 令 room 重投。
func (rt *Runtime) offlinePush(uid int64, msg OfflineMsg, done func(int32)) {
	if rt.offlineStore == nil {
		done(0)
		return
	}
	rt.offlineStore.Push(rt.tq, uid, msg, func(err error) {
		if err != nil {
			logger.Warn("settle offline push failed", logger.Int64("uid", uid), logger.String("gameId", msg.OpID), logger.Err(err))
			done(1)
			return
		}
		done(0)
	})
}
