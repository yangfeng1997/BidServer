package internal

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"project/pkg/taskqueue"
	"project/pkg/timewheel"
)

const (
	mailListLimit           = 50
	coalesceFlushInterval   = time.Second
	touchThrottle           = 2 * time.Minute
	drainTimeout            = 5 * time.Second
	defaultRuntimeQueueSize = 1024
)

var ErrStoreNotConfigured = errors.New("lobby runtime store is not configured")

// LoginResult 是 Runtime 登录领域结果，不绑定客户端协议。
type LoginResult struct {
	Code        int32
	UID         int64
	LobbyNodeID string
}

// RejoinResult 是 room rejoin hook 的协议无关结果。
type RejoinResult struct {
	Code     int32
	HB       int64
	HBR      int64
	Remain   int32
	ItemID   int32
	Currency string
}

// MatchRequest 是发起匹配的协议无关请求。
type MatchRequest struct {
	UID         int64
	ReqID       string
	MMR         int64
	LobbyNodeID string
}

// RuntimeConfig 主循环运行时配置。
type RuntimeConfig struct {
	NodeID        string
	Store         DocStore
	MailStore     MailStore
	OfflineStore  OfflineStore
	QueueSize     int
	Tick          time.Duration
	FlushInterval time.Duration
}

// Runtime lobby 单主循环运行时：串行承载全部玩家 EC 逻辑。
type Runtime struct {
	nodeID       string
	store        DocStore
	mailStore    MailStore
	offlineStore OfflineStore
	events       *Events
	tq           *taskqueue.Queue
	tw           *timewheel.TimeWheel
	players      map[int64]*Player
	dirtyFlush   map[int64]bool

	tick          time.Duration
	flushInterval time.Duration
	stopCh        chan struct{}
	doneCh        chan struct{}
	stopOnce      sync.Once
	startOnce     sync.Once
	inflight      atomic.Int64

	reqSeq int64

	// TODO: RPC messages are not supported yet; wire these hooks when lobby remote proto is restored.
	onlineRegister   func(uid int64, gatewayNodeID string)
	onlineUnregister func(uid int64)
	onlineTouch      func(uid int64)
	publishMatch     func(req MatchRequest) error
	bindRoom         func(uid int64, roomNodeID, gameID string)
	unbindRoom       func(uid int64)
	forwardBid       func(uid int64, roomNodeID, gameID string, amount int64, done func(code int32, highest int64))
	queryOnline      func(uid int64, done func(roomNodeID, gameID string))
	rejoinRoom       func(uid int64, roomNodeID, gameID, newLobbyNode string, done func(RejoinResult))
	queryGame        func(roomNodeID, gameID string, done func(alive bool))
}

func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultRuntimeQueueSize
	}
	if cfg.Tick <= 0 {
		cfg.Tick = 100 * time.Millisecond
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 30 * time.Second
	}
	rt := &Runtime{
		nodeID:        cfg.NodeID,
		store:         cfg.Store,
		mailStore:     cfg.MailStore,
		offlineStore:  cfg.OfflineStore,
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
	rt.publishMatch = rt.publishMatchNoop
	rt.bindRoom = rt.bindRoomNoop
	rt.unbindRoom = rt.unbindRoomNoop
	rt.forwardBid = rt.forwardBidNoop
	rt.queryOnline = rt.queryOnlineNoop
	rt.rejoinRoom = rt.rejoinRoomNoop
	rt.queryGame = rt.queryGameNoop
	return rt
}

func (rt *Runtime) Events() *Events { return rt.events }

// Submit 跨 goroutine 把 fn 投递到主循环串行执行。
func (rt *Runtime) Submit(fn func()) { rt.tq.Enqueue(fn) }

// Start 注册周期 flush 并启动主循环 goroutine。
func (rt *Runtime) Start() {
	rt.startOnce.Do(func() {
		rt.tw.Tick(rt.flushInterval, rt.flushAllDirty)
		rt.tw.Tick(coalesceFlushInterval, rt.coalesceFlush)
		go rt.loop()
	})
}

// Stop 停止主循环并等待退出，可重复调用。
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

func (rt *Runtime) drain() {
	deadline := time.After(drainTimeout)
	for rt.inflight.Load() > 0 {
		select {
		case fn := <-rt.tq.C():
			fn()
		case <-deadline:
			return
		}
	}
}

// Login 主循环内登录：加载/建档玩家工作副本 + 在线注册 + reply。
func (rt *Runtime) Login(uid int64, gatewayNodeID string, reply func(LoginResult, error)) {
	if _, ok := rt.players[uid]; ok {
		rt.onlineRegister(uid, gatewayNodeID)
		reply(LoginResult{Code: 0, UID: uid, LobbyNodeID: rt.nodeID}, nil)
		return
	}
	if rt.store == nil {
		reply(LoginResult{}, ErrStoreNotConfigured)
		return
	}
	rt.store.Load(rt.tq, uid, func(doc *PlayerDoc, found bool, err error) {
		if err != nil {
			reply(LoginResult{}, err)
			return
		}
		if !found || doc == nil {
			doc = NewPlayerDoc(uid)
		}
		rt.players[uid] = buildPlayer(uid, doc)
		rt.players[uid].attachMail(rt.mailStore)
		rt.events.PlayerLoaded.Publish(PlayerLoaded{UID: uid})
		p := rt.players[uid]
		rt.replayOffline(uid, p, func() {
			rt.onlineRegister(uid, gatewayNodeID)
			reply(LoginResult{Code: 0, UID: uid, LobbyNodeID: rt.nodeID}, nil)
			rt.tryReconnect(uid)
			rt.scanFriendAccepts(uid, p, nil)
			// TODO: client protocol push is intentionally not ported in this migration.
		})
	})
}

// Disconnect 主循环内断连：flush 后剔除内存副本；非 in-game 立即注销在线。
func (rt *Runtime) Disconnect(uid int64) {
	p, ok := rt.players[uid]
	inGame := false
	if ok {
		inGame = p.RoomAffinity() != nil
		// TODO: client protocol push is intentionally not ported in this migration.
		rt.flushPlayer(uid, p, func(ok bool) {
			if ok {
				delete(rt.players, uid)
			}
		})
	}
	if !inGame {
		rt.onlineUnregister(uid)
	}
}

// Player 主循环内取玩家（不存在返回 nil）。
func (rt *Runtime) Player(uid int64) *Player { return rt.players[uid] }

// NotifyNewMail 只保留领域入口；客户端 push 本轮不移植。
func (rt *Runtime) NotifyNewMail(to, from int64, mailType string) {
	// TODO: client protocol push is intentionally not ported in this migration.
}

func (rt *Runtime) flushAllDirty() {
	for uid, p := range rt.players {
		rt.flushPlayer(uid, p, nil)
	}
}

func (rt *Runtime) flushPlayer(uid int64, p *Player, after func(ok bool)) {
	if rt.store == nil {
		if after != nil {
			after(false)
		}
		return
	}
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
	if len(fields) == 0 {
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
				c.MarkDirty()
			}
		}
		if after != nil {
			after(ok)
		}
	})
}

func (rt *Runtime) PublishCurrencyChanged(uid int64, kind string, delta int64) {
	rt.events.CurrencyChanged.Publish(CurrencyChanged{UID: uid, Kind: kind, Delta: delta})
}

func (rt *Runtime) FlushSoon(uid int64) { rt.dirtyFlush[uid] = true }

func (rt *Runtime) coalesceFlush() {
	for uid := range rt.dirtyFlush {
		if p, ok := rt.players[uid]; ok {
			rt.flushPlayer(uid, p, nil)
		}
		delete(rt.dirtyFlush, uid)
	}
}

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

func (rt *Runtime) scanFriendAccepts(uid int64, p *Player, after func()) {
	if rt.mailStore == nil {
		if after != nil {
			after()
		}
		return
	}
	rt.mailStore.PendingFriendAccepts(rt.tq, uid, func(mails []MailDoc, err error) {
		if err != nil {
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

func (rt *Runtime) StartMatch(uid, mmr int64) {
	if rt.publishMatch == nil {
		return
	}
	rt.reqSeq++
	req := MatchRequest{
		UID:         uid,
		ReqID:       fmt.Sprintf("%s-%d-%d", rt.nodeID, uid, rt.reqSeq),
		MMR:         mmr,
		LobbyNodeID: rt.nodeID,
	}
	pub := rt.publishMatch
	go func() { _ = pub(req) }()
}

func (rt *Runtime) BindRoom(uid int64, roomNodeID, gameID string) {
	if rt.bindRoom != nil {
		rt.bindRoom(uid, roomNodeID, gameID)
	}
	if p := rt.Player(uid); p != nil {
		p.SetRoomAffinity(roomNodeID, gameID, "")
	}
}

func (rt *Runtime) PushMatchFound(uid int64, roomNodeID, gameID string) {
	// TODO: client protocol push is intentionally not ported in this migration.
}

func (rt *Runtime) PushAuctionState(uid int64, gameID string, hb, hbr int64, rem int32) {
	// TODO: client protocol push is intentionally not ported in this migration.
}

func (rt *Runtime) PushAuctionResult(uid int64, gameID string, winner, price int64, currency string, itemID int32) {
	// TODO: client protocol push is intentionally not ported in this migration.
}

func (rt *Runtime) PushMatchTimeout(uid int64) {
	// TODO: client protocol push is intentionally not ported in this migration.
}

func (rt *Runtime) PushReconnectAuction(uid int64, gameID string, hb, hbr int64, rem int32, itemID int32, currency string, status int32) {
	// TODO: client protocol push is intentionally not ported in this migration.
}

func (rt *Runtime) replayOffline(uid int64, p *Player, after func()) {
	if rt.offlineStore == nil {
		if after != nil {
			after()
		}
		return
	}
	rt.offlineStore.Load(rt.tq, uid, func(msgs []OfflineMsg, err error) {
		if err != nil {
			if after != nil {
				after()
			}
			return
		}
		acked := make([]string, 0, len(msgs))
		for _, msg := range msgs {
			rt.applyOfflineMsg(uid, p, msg)
			if msg.OpID != "" {
				acked = append(acked, msg.OpID)
			}
		}
		if len(acked) == 0 {
			if after != nil {
				after()
			}
			return
		}
		rt.flushPlayer(uid, p, func(ok bool) {
			if !ok {
				if after != nil {
					after()
				}
				return
			}
			rt.offlineStore.Ack(rt.tq, uid, acked, func(error) {
				if after != nil {
					after()
				}
			})
		})
	})
}

func (rt *Runtime) applyOfflineMsg(uid int64, p *Player, m OfflineMsg) {
	switch m.Type {
	case OfflineMsgSettle:
		if m.Price > 0 {
			p.Currency().Spend(m.OpID+":spend", m.Currency, m.Price)
		}
		if m.ItemID != 0 {
			p.Bag().Add(m.OpID+":item", m.ItemID, 1)
		}
		if m.Price != 0 {
			rt.PublishCurrencyChanged(uid, m.Currency, -m.Price)
		}
	}
}

func (rt *Runtime) tryReconnect(uid int64) {
	if rt.queryOnline == nil || rt.rejoinRoom == nil || rt.queryGame == nil {
		return
	}
	rt.queryOnline(uid, func(roomNodeID, gameID string) {
		if roomNodeID == "" || gameID == "" {
			return
		}
		rt.queryGame(roomNodeID, gameID, func(alive bool) {
			if !alive {
				rt.unbindRoom(uid)
				return
			}
			rt.rejoinRoom(uid, roomNodeID, gameID, rt.nodeID, func(result RejoinResult) {
				if result.Code != 0 {
					rt.unbindRoom(uid)
					return
				}
				if p := rt.Player(uid); p != nil {
					p.SetRoomAffinity(roomNodeID, gameID, result.Currency)
				}
				rt.PushReconnectAuction(uid, gameID, result.HB, result.HBR, result.Remain, result.ItemID, result.Currency, result.Code)
			})
		})
	})
}

func (rt *Runtime) Settle(uid int64, gameID string, winner, price int64, currency string, itemID int32, done func(int32)) {
	p := rt.Player(uid)
	if p == nil {
		rt.offlinePush(uid, OfflineMsg{Type: OfflineMsgSettle, OpID: gameID, Price: price, Currency: currency, ItemID: itemID}, done)
		return
	}
	if winner == uid {
		if _, ok := p.Currency().Spend(gameID+":settle", currency, price); !ok {
			if done != nil {
				done(1)
			}
			return
		}
		p.Bag().Add(gameID+":item", itemID, 1)
		rt.PublishCurrencyChanged(uid, currency, -price)
	}
	p.ClearRoomAffinity()
	rt.unbindRoom(uid)
	rt.FlushSoon(uid)
	if done != nil {
		done(0)
	}
}

func (rt *Runtime) offlinePush(uid int64, msg OfflineMsg, done func(int32)) {
	if rt.offlineStore == nil {
		if done != nil {
			done(1)
		}
		return
	}
	rt.offlineStore.Push(rt.tq, uid, msg, func(err error) {
		if done != nil {
			if err != nil {
				done(1)
				return
			}
			done(0)
		}
	})
}

func (rt *Runtime) registerOnline(uid int64, gatewayNodeID string) {
	// TODO: RPC messages are not supported yet; wire online registration here when lobby remote proto is restored.
}

func (rt *Runtime) unregisterOnline(uid int64) {
	// TODO: RPC messages are not supported yet; wire online unregister here when lobby remote proto is restored.
}

func (rt *Runtime) touchOnline(uid int64) {
	// TODO: RPC messages are not supported yet; wire online touch here when lobby remote proto is restored.
}

func (rt *Runtime) publishMatchNoop(req MatchRequest) error {
	// TODO: RPC messages are not supported yet; wire match publish here when router/match proto is restored.
	return nil
}

func (rt *Runtime) bindRoomNoop(uid int64, roomNodeID, gameID string) {
	// TODO: RPC messages are not supported yet; wire online bind-room here when lobby remote proto is restored.
}

func (rt *Runtime) unbindRoomNoop(uid int64) {
	// TODO: RPC messages are not supported yet; wire online unbind-room here when lobby remote proto is restored.
}

func (rt *Runtime) forwardBidNoop(uid int64, roomNodeID, gameID string, amount int64, done func(code int32, highest int64)) {
	// TODO: RPC messages are not supported yet; wire room bid forwarding here when room proto is restored.
	if done != nil {
		done(1, 0)
	}
}

func (rt *Runtime) queryOnlineNoop(uid int64, done func(roomNodeID, gameID string)) {
	// TODO: RPC messages are not supported yet; wire online query here when lobby remote proto is restored.
	if done != nil {
		done("", "")
	}
}

func (rt *Runtime) rejoinRoomNoop(uid int64, roomNodeID, gameID, newLobbyNode string, done func(RejoinResult)) {
	// TODO: RPC messages are not supported yet; wire room rejoin here when room proto is restored.
	if done != nil {
		done(RejoinResult{Code: 1})
	}
}

func (rt *Runtime) queryGameNoop(roomNodeID, gameID string, done func(alive bool)) {
	// TODO: RPC messages are not supported yet; wire game query here when game proto is restored.
	if done != nil {
		done(false)
	}
}
