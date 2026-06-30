package internal

import (
	"context"

	roompb "project/protocal/gen/room"
	"project/src/framework/cluster"
)

// RoomHandler roomsvr 集群 RPC handler 薄壳：捕获 Replier + Submit 进主循环 + 延迟回包。
type RoomHandler struct {
	rt *Runtime
}

// NewRoomHandler 构造 RoomHandler
func NewRoomHandler(rt *Runtime) *RoomHandler { return &RoomHandler{rt: rt} }

// Bid route="RoomHandler.bid"：记价 + 更新最高价 + 接受则广播。
func (h *RoomHandler) Bid(ctx context.Context, req *roompb.RPC_Bid_Req) (*roompb.RPC_Bid_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	gameID, uid, amount := req.GameId, req.Uid, req.Amount
	h.rt.Submit(func() {
		code, hb, hbr := h.rt.Bid(gameID, uid, amount)
		if code == 0 {
			h.rt.broadcastState(gameID)
		}
		replyProto(replier, &roompb.RPC_Bid_Rsp{Code: code, HighestBid: hb, HighestBidder: hbr}, nil)
	})
	return nil, cluster.ErrDeferredReply
}

// Opengame route="RoomHandler.opengame"：按 gameId 建局（幂等），Rsp 回带 room_node_id=self。
func (h *RoomHandler) Opengame(ctx context.Context, req *roompb.RPC_OpenGame_Req) (*roompb.RPC_OpenGame_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	if req.GameId == "" || len(req.Participants) == 0 {
		h.rt.Submit(func() { replyProto(replier, &roompb.RPC_OpenGame_Rsp{Code: 1}, nil) })
		return nil, cluster.ErrDeferredReply
	}
	parts := make([]Participant, 0, len(req.Participants))
	for _, p := range req.Participants {
		parts = append(parts, Participant{UID: p.Uid, LobbyNodeID: p.LobbyNodeId})
	}
	gameID, itemID, countdown, currency := req.GameId, req.ItemId, req.CountdownSec, req.Currency
	h.rt.Submit(func() {
		h.rt.OpenGame(gameID, itemID, countdown, currency, parts)
		replyProto(replier, &roompb.RPC_OpenGame_Rsp{Code: 0, RoomNodeId: h.rt.NodeID()}, nil)
	})
	return nil, cluster.ErrDeferredReply
}

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
