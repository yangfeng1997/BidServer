package internal

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"google.golang.org/protobuf/proto"
	lobbypb "project/protocal/gen/lobby"
	matchpb "project/protocal/gen/match"
	roompb "project/protocal/gen/room"
	"project/src/common/logger"
	"project/src/framework/cluster"
)

// LobbyHandler lobby 集群 RPC handler 薄壳：捕获 Replier + 把工作 Submit 进主循环 +
// 返回延迟回包哨兵；主循环内显式调用业务逻辑并经 Replier 异步回包。
type LobbyHandler struct {
	rt *Runtime
}

func NewLobbyHandler(rt *Runtime) *LobbyHandler { return &LobbyHandler{rt: rt} }

// Login route="LobbyHandler.login"
func (h *LobbyHandler) Login(ctx context.Context, req *lobbypb.RPC_Login_Req) (*lobbypb.RPC_Login_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	var gatewayNodeID string
	if cs := cluster.SessionFromCtx(ctx); cs != nil {
		gatewayNodeID = cs.FrontendId
	}
	h.rt.Submit(func() {
		uid, ok := verifyToken(req.Token)
		if !ok {
			replyProto(replier, &lobbypb.RPC_Login_Rsp{Code: -1}, nil)
			return
		}
		h.rt.Login(uid, gatewayNodeID, func(rsp *lobbypb.RPC_Login_Rsp, err error) {
			replyProto(replier, rsp, err)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// PlayerDisconnect route="LobbyHandler.playerdisconnect"（Notify，无回包）
func (h *LobbyHandler) PlayerDisconnect(_ context.Context, req *lobbypb.RPC_PlayerDisconnect_Notify) {
	uid := req.Uid
	h.rt.Submit(func() { h.rt.Disconnect(uid) })
}

// Touch route="LobbyHandler.touch"（Notify，无回包）
func (h *LobbyHandler) Touch(_ context.Context, req *lobbypb.RPC_Touch_Notify) {
	uid := req.Uid
	h.rt.Submit(func() { h.rt.Touch(uid) })
}

// Additem route="LobbyHandler.additem"
func (h *LobbyHandler) Additem(ctx context.Context, req *lobbypb.CS_AddItem) (*lobbypb.SC_AddItem, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		n := p.Bag().Add(req.OpId, req.ItemId, req.Count)
		replyProto(replier, &lobbypb.SC_AddItem{ItemId: req.ItemId, Count: n}, nil)
	})
	return nil, cluster.ErrDeferredReply
}

// Baglist route="LobbyHandler.baglist"
func (h *LobbyHandler) Baglist(ctx context.Context, _ *lobbypb.CS_BagList) (*lobbypb.SC_BagList, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		items := p.Bag().Items()
		rsp := &lobbypb.SC_BagList{Items: make([]*lobbypb.BagItem, 0, len(items))}
		for id, c := range items {
			rsp.Items = append(rsp.Items, &lobbypb.BagItem{ItemId: id, Count: c})
		}
		replyProto(replier, rsp, nil)
	})
	return nil, cluster.ErrDeferredReply
}

// Currencyquery route="LobbyHandler.currencyquery"
func (h *LobbyHandler) Currencyquery(ctx context.Context, _ *lobbypb.CS_CurrencyQuery) (*lobbypb.SC_CurrencyQuery, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		rsp := &lobbypb.SC_CurrencyQuery{}
		for kind, amt := range p.Currency().Balances() {
			rsp.Balances = append(rsp.Balances, &lobbypb.CurrencyAmount{Kind: kind, Amount: amt})
		}
		replyProto(replier, rsp, nil)
	})
	return nil, cluster.ErrDeferredReply
}

// Purchase route="LobbyHandler.purchase"：显式编排 扣币+加道具（同 op_id 幂等）
func (h *LobbyHandler) Purchase(ctx context.Context, req *lobbypb.CS_Purchase) (*lobbypb.SC_Purchase, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
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
		cur := p.Currency()
		if !cur.CanAfford(req.Kind, req.Price) {
			replyProto(replier, &lobbypb.SC_Purchase{Code: 1, Balance: cur.Balance(req.Kind)}, nil)
			return
		}
		bal, ok := cur.Spend(req.OpId, req.Kind, req.Price)
		if !ok { // 理论不达（已 CanAfford），防御
			replyProto(replier, &lobbypb.SC_Purchase{Code: 1, Balance: bal}, nil)
			return
		}
		h.rt.PublishCurrencyChanged(uid, req.Kind, -req.Price)
		n := p.Bag().Add(req.OpId, req.ItemId, 1)
		h.rt.FlushSoon(uid)
		replyProto(replier, &lobbypb.SC_Purchase{Code: 0, Balance: bal, ItemCount: int32(n)}, nil)
	})
	return nil, cluster.ErrDeferredReply
}

func uidFromCtx(ctx context.Context) int64 {
	if cs := cluster.SessionFromCtx(ctx); cs != nil {
		return cs.Uid
	}
	return 0
}

// replyProto marshal 业务响应并经 Replier 异步回包（err 非 nil 时回错误响应）
func replyProto(r cluster.Replier, msg proto.Message, err error) {
	if r == nil {
		return
	}
	if err != nil {
		r.Reply(nil, err)
		return
	}
	data, merr := proto.Marshal(msg)
	if merr != nil {
		r.Reply(nil, merr)
		return
	}
	r.Reply(data, nil)
}

// verifyToken P3a stub：非空即通过；token 为正整数时即作 uid（便于多玩家测试），
// 否则回退 10001。后续阶段替换为真实无状态 token 校验。
func verifyToken(token string) (int64, bool) {
	if token == "" {
		return 0, false
	}
	if uid, err := strconv.ParseInt(token, 10, 64); err == nil && uid > 0 {
		return uid, true
	}
	return 10001, true
}

// Maillist route="LobbyHandler.maillist"
func (h *LobbyHandler) Maillist(ctx context.Context, _ *lobbypb.CS_MailList) (*lobbypb.SC_MailList, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		p.Mail().List(h.rt.tq, mailListLimit, func(mails []MailDoc, err error) {
			if err != nil {
				replyProto(replier, nil, err)
				return
			}
			rsp := &lobbypb.SC_MailList{}
			for _, m := range mails {
				rsp.Mails = append(rsp.Mails, mailToProto(m))
			}
			replyProto(replier, rsp, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// Mailclaim route="LobbyHandler.mailclaim"：① 重排：get→grant(opID=mailID 持久幂等)→flush→mark-claimed
func (h *LobbyHandler) Mailclaim(ctx context.Context, req *lobbypb.CS_MailClaim) (*lobbypb.SC_MailClaim, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	id, err := primitive.ObjectIDFromHex(req.MailId)
	if err != nil {
		h.rt.Submit(func() { replyProto(replier, &lobbypb.SC_MailClaim{Code: 1}, nil) })
		return nil, cluster.ErrDeferredReply
	}
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		opID := req.MailId // 持久幂等键（hex）
		p.Mail().Get(h.rt.tq, id, func(ok bool, m *MailDoc, gerr error) {
			if gerr != nil {
				replyProto(replier, nil, gerr)
				return
			}
			if !ok {
				replyProto(replier, &lobbypb.SC_MailClaim{Code: 1}, nil) // 不存在/已领取
				return
			}
			atts := m.Attachments
			h.rt.grantAttachments(uid, p, opID, atts) // 幂等：重领经 ops(mailID) 跳过
			h.rt.flushPlayer(uid, p, func(ok bool) {
				if !ok {
					replyProto(replier, &lobbypb.SC_MailClaim{Code: 1}, nil) // 落库失败：客户端重领（opID=mailID 持久幂等）
					return
				}
				p.Mail().MarkClaimed(h.rt.tq, id, func(merr error) {
					if merr != nil {
						logger.Warn("mailclaim: mark claimed failed (grant 已落，最终一致)",
							logger.Int64("uid", uid), logger.String("mailId", opID), logger.Err(merr))
					}
				})
				rsp := &lobbypb.SC_MailClaim{Code: 0}
				for _, a := range atts {
					rsp.Granted = append(rsp.Granted, &lobbypb.Attachment{Kind: a.Kind, Id: a.ID, Count: a.Count})
				}
				replyProto(replier, rsp, nil)
			})
		})
	})
	return nil, cluster.ErrDeferredReply
}

// Friendadd route="LobbyHandler.friendadd"：投递 friend_req 邮件给目标
func (h *LobbyHandler) Friendadd(ctx context.Context, req *lobbypb.CS_FriendAdd) (*lobbypb.SC_FriendAdd, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		if req.Target == uid || req.Target <= 0 {
			replyProto(replier, &lobbypb.SC_FriendAdd{Code: 1}, nil)
			return
		}
		if p.Friend().Has(req.Target) {
			replyProto(replier, &lobbypb.SC_FriendAdd{Code: 2}, nil)
			return
		}
		h.rt.mailStore.Insert(h.rt.tq, &MailDoc{
			To: req.Target, From: uid, Type: MailTypeFriendReq, Ts: time.Now().UnixNano(),
		}, func(err error) {
			if err != nil {
				replyProto(replier, nil, err)
				return
			}
			h.rt.NotifyNewMail(req.Target, uid, MailTypeFriendReq)
			replyProto(replier, &lobbypb.SC_FriendAdd{Code: 0}, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// Friendrespond route="LobbyHandler.friendrespond"：claim friend_req；accept 则加好友 + 回投 friend_accept
func (h *LobbyHandler) Friendrespond(ctx context.Context, req *lobbypb.CS_FriendRespond) (*lobbypb.SC_FriendRespond, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	id, err := primitive.ObjectIDFromHex(req.MailId)
	if err != nil {
		h.rt.Submit(func() { replyProto(replier, &lobbypb.SC_FriendRespond{Code: 1}, nil) })
		return nil, cluster.ErrDeferredReply
	}
	accept := req.Accept
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		p.Mail().Claim(h.rt.tq, id, func(ok bool, m *MailDoc, cerr error) {
			if cerr != nil {
				replyProto(replier, nil, cerr)
				return
			}
			if !ok || m.Type != MailTypeFriendReq {
				replyProto(replier, &lobbypb.SC_FriendRespond{Code: 1}, nil)
				return
			}
			if accept {
				p.Friend().Add(m.From)
				h.rt.FlushSoon(uid)
				h.rt.mailStore.Insert(h.rt.tq, &MailDoc{
					To: m.From, From: uid, Type: MailTypeFriendAccept, Ts: time.Now().UnixNano(),
				}, func(err error) {
					if err != nil {
						logger.Warn("friend respond: insert friend_accept failed",
							logger.Int64("from", uid), logger.Int64("to", m.From), logger.Err(err))
						return
					}
					h.rt.NotifyNewMail(m.From, uid, MailTypeFriendAccept)
				})
			}
			replyProto(replier, &lobbypb.SC_FriendRespond{Code: 0}, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// Friendremove route="LobbyHandler.friendremove"：单边删除（MVP）
func (h *LobbyHandler) Friendremove(ctx context.Context, req *lobbypb.CS_FriendRemove) (*lobbypb.SC_FriendRemove, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		if p.Friend().Remove(req.Target) {
			h.rt.FlushSoon(uid)
		}
		replyProto(replier, &lobbypb.SC_FriendRemove{Code: 0}, nil)
	})
	return nil, cluster.ErrDeferredReply
}

// Startmatch route="LobbyHandler.startmatch"：校验可匹配 + 读 mmr + off-loop 发布匹配请求
func (h *LobbyHandler) Startmatch(ctx context.Context, _ *lobbypb.CS_StartMatch) (*lobbypb.SC_StartMatch, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, &lobbypb.SC_StartMatch{Code: -1}, nil)
			return
		}
		if p.RoomAffinity() != nil {
			replyProto(replier, &lobbypb.SC_StartMatch{Code: 1}, nil) // 已在局中
			return
		}
		h.rt.StartMatch(uid, p.Rating().MMR())
		replyProto(replier, &lobbypb.SC_StartMatch{Code: 0}, nil)
	})
	return nil, cluster.ErrDeferredReply
}

// Gamestarted route="LobbyHandler.gamestarted"：match→lobby 开局回告（经 router DIRECT 到本节点，同步 RPC 返回 ack）
func (h *LobbyHandler) Gamestarted(ctx context.Context, req *matchpb.RPC_GameStarted_Req) (*matchpb.RPC_GameStarted_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid, gameID, roomNodeID, currency := req.Uid, req.GameId, req.RoomNodeId, req.Currency
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, &matchpb.RPC_GameStarted_Rsp{Code: -1}, nil)
			return
		}
		p.SetRoomAffinity(roomNodeID, gameID, currency) // 内存亲和（绝对写，幂等）
		h.rt.BindRoom(uid, roomNodeID, gameID)          // off-loop 同步 online
		h.rt.PushMatchFound(uid, roomNodeID, gameID)    // off-loop 推 SC_MatchFound
		replyProto(replier, &matchpb.RPC_GameStarted_Rsp{Code: 0}, nil)
	})
	return nil, cluster.ErrDeferredReply
}

// Friendlist route="LobbyHandler.friendlist"：off-loop 逐好友查在线，回 SC_FriendList
func (h *LobbyHandler) Friendlist(ctx context.Context, _ *lobbypb.CS_FriendList) (*lobbypb.SC_FriendList, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		friends := p.Friend().List()
		pc := h.rt.presence
		if pc == nil {
			replyProto(replier, &lobbypb.SC_FriendList{}, nil)
			return
		}
		go func() {
			rsp := snapshotFriends(pc, friends)
			replyProto(replier, rsp, nil)
		}()
	})
	return nil, cluster.ErrDeferredReply
}

// Bid route="LobbyHandler.bid"：校验亲和 + CanAfford → off-loop 转发 room。
func (h *LobbyHandler) Bid(ctx context.Context, req *lobbypb.CS_Bid) (*lobbypb.SC_Bid, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	gameID, amount := req.GameId, req.Amount
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, &lobbypb.SC_Bid{Code: 2}, nil)
			return
		}
		aff := p.RoomAffinity()
		if aff == nil || aff.gameID != gameID {
			replyProto(replier, &lobbypb.SC_Bid{Code: 2}, nil) // 非局中/亲和不符
			return
		}
		if amount <= 0 || !p.Currency().CanAfford(aff.currency, amount) {
			replyProto(replier, &lobbypb.SC_Bid{Code: 4}, nil) // 余额不足/非法额
			return
		}
		if h.rt.forwardBid == nil {
			replyProto(replier, &lobbypb.SC_Bid{Code: 2}, nil)
			return
		}
		h.rt.forwardBid(uid, aff.roomNodeID, gameID, amount, func(code int32, highest int64) {
			replyProto(replier, &lobbypb.SC_Bid{Code: code, HighestBid: highest}, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// Matchtimeout route="LobbyHandler.matchtimeout"（matchsvr→lobby Cast）：推 SC_MatchTimeout。
func (h *LobbyHandler) Matchtimeout(_ context.Context, req *matchpb.RPC_MatchTimeout_Notify) {
	uid := req.Uid
	h.rt.Submit(func() { h.rt.PushMatchTimeout(uid) })
}

// Auctionstate route="LobbyHandler.auctionstate"（room→lobby Cast，无回包）：推 SC_AuctionState 给客户端。
func (h *LobbyHandler) Auctionstate(_ context.Context, req *roompb.RPC_AuctionState_Notify) {
	uid, gameID, hb, hbr, rem := req.Uid, req.GameId, req.HighestBid, req.HighestBidder, req.CountdownRemaining
	h.rt.Submit(func() { h.rt.PushAuctionState(uid, gameID, hb, hbr, rem) })
}

// Settle route="LobbyHandler.settle"（room→lobby CallViaSync）：结算落地，ack 经持久落库后发出（§6.6）。
func (h *LobbyHandler) Settle(ctx context.Context, req *roompb.RPC_Settle_Req) (*roompb.RPC_Settle_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid, gameID, winner, price, currency, itemID := req.Uid, req.GameId, req.Winner, req.Price, req.Currency, req.ItemId
	h.rt.Submit(func() {
		h.rt.Settle(uid, gameID, winner, price, currency, itemID, func(code int32) {
			replyProto(replier, &roompb.RPC_Settle_Rsp{Code: code}, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// mailToProto MailDoc → proto MailItem
func mailToProto(m MailDoc) *lobbypb.MailItem {
	mi := &lobbypb.MailItem{
		MailId: m.ID.Hex(), From: m.From, Type: m.Type, Body: m.Body, Ts: m.Ts, Claimed: m.Claimed,
	}
	for _, a := range m.Attachments {
		mi.Attachments = append(mi.Attachments, &lobbypb.Attachment{Kind: a.Kind, Id: a.ID, Count: a.Count})
	}
	return mi
}
