// src/servers/lobbysvr/internal/presence.go
package internal

import (
	"context"
	"strconv"

	gatepb "project/protocal/gen/gate"
	lobbypb "project/protocal/gen/lobby"
	onlinepb "project/protocal/gen/online"
	routerpb "project/protocal/gen/router"
	"project/src/common/logger"
	"project/src/common/serialize/protobuf"
	"project/src/framework/cluster"
	"project/src/framework/cluster/routerclient"
)

const (
	msgIDSCFriendPresence   uint32 = 2031
	msgIDSCMailNew          uint32 = 2033
	msgIDSCMatchFound       uint32 = 2036
	msgIDSCAuctionState     uint32 = 2039
	msgIDSCAuctionResult    uint32 = 2040
	msgIDSCMatchTimeout     uint32 = 2041
	msgIDSCReconnectAuction uint32 = 2042
)

var clientSerializer = protobuf.NewSerializer() // 推送 body 用 client 序列化器(proto，全链路 proto)

// presenceClient 抽象在线查询与客户端推送，便于单测注入 fake。
type presenceClient interface {
	Query(uid int64) (gatewayNodeID string, online bool)
	Push(gatewayNodeID string, uid int64, msgID uint32, body []byte)
}

// clusterPresence 基于真实 router/gate 的 presenceClient
type clusterPresence struct{ cls cluster.Cluster }

func (c clusterPresence) Query(uid int64) (string, bool) {
	ctx := cluster.WithCluster(context.Background(), c.cls)
	rsp, err := routerclient.CallViaSync[*onlinepb.RPC_Query_Rsp](
		ctx, c.cls, "onlinesvr",
		routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
		"OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: uid})
	if err != nil {
		logger.Warn("presence query failed", logger.Int64("uid", uid), logger.Err(err))
		return "", false
	}
	if !rsp.Online || rsp.Entry == nil {
		return "", false
	}
	return rsp.Entry.GatewayNodeId, true
}

func (c clusterPresence) Push(gatewayNodeID string, uid int64, msgID uint32, body []byte) {
	gw, err := cluster.ParseNodeID(gatewayNodeID)
	if err != nil {
		logger.Warn("presence push: bad gateway nodeID", logger.String("nodeID", gatewayNodeID))
		return
	}
	if err := c.cls.Cast(context.Background(), gw, "GateHandler.pushtoclient",
		&gatepb.RPC_PushToClient{Uid: uid, MsgId: msgID, Body: body}); err != nil {
		logger.Warn("presence push: cast failed", logger.Int64("uid", uid), logger.Err(err))
	}
}

// fanoutPresence 对每个 friend 查在线，在线则推 SC_FriendPresence{self, online}。
// 同步执行（调用方在 off-loop goroutine 调用，避免阻塞主循环）。
func fanoutPresence(pc presenceClient, self int64, friends []int64, online bool) {
	if len(friends) == 0 {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_FriendPresence{Uid: self, Online: online})
	if err != nil {
		logger.Warn("presence: marshal body failed", logger.Err(err))
		return
	}
	for _, f := range friends {
		if gw, ok := pc.Query(f); ok {
			pc.Push(gw, f, msgIDSCFriendPresence, body)
		}
	}
}

// notifyNewMail 若收件人在线，推 SC_MailNew{from, type}（同步，off-loop 调用）。
func notifyNewMail(pc presenceClient, to, from int64, mailType string) {
	gw, online := pc.Query(to)
	if !online {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_MailNew{From: from, Type: mailType})
	if err != nil {
		logger.Warn("notify new mail: marshal failed", logger.Int64("to", to), logger.Err(err))
		return
	}
	pc.Push(gw, to, msgIDSCMailNew, body)
}

// pushMatchFound 若玩家在线，推 SC_MatchFound{room, game}（同步，off-loop 调用）。
func pushMatchFound(pc presenceClient, uid int64, roomNodeID, gameID string) {
	gw, online := pc.Query(uid)
	if !online {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_MatchFound{RoomNodeId: roomNodeID, GameId: gameID})
	if err != nil {
		logger.Warn("push match found: marshal failed", logger.Int64("uid", uid), logger.Err(err))
		return
	}
	pc.Push(gw, uid, msgIDSCMatchFound, body)
}

// snapshotFriends 逐好友查在线，构造 SC_FriendList（同步，off-loop 调用）。
func snapshotFriends(pc presenceClient, friends []int64) *lobbypb.SC_FriendList {
	rsp := &lobbypb.SC_FriendList{}
	for _, f := range friends {
		_, online := pc.Query(f)
		rsp.Friends = append(rsp.Friends, &lobbypb.FriendEntry{Uid: f, Online: online})
	}
	return rsp
}

// pushAuctionState 若玩家在线，推 SC_AuctionState（同步，off-loop 调用）。
func pushAuctionState(pc presenceClient, uid int64, gameID string, hb, hbr int64, rem int32) {
	gw, online := pc.Query(uid)
	if !online {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_AuctionState{
		GameId: gameID, HighestBid: hb, HighestBidder: hbr, CountdownRemaining: rem,
	})
	if err != nil {
		logger.Warn("push auction state: marshal failed", logger.Int64("uid", uid), logger.Err(err))
		return
	}
	pc.Push(gw, uid, msgIDSCAuctionState, body)
}

// pushAuctionResult 若玩家在线，推 SC_AuctionResult（同步，off-loop 调用）。
func pushAuctionResult(pc presenceClient, uid int64, gameID string, winner, price int64, currency string, itemID int32) {
	gw, online := pc.Query(uid)
	if !online {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_AuctionResult{
		GameId: gameID, Winner: winner, Price: price, ItemId: itemID, Currency: currency,
	})
	if err != nil {
		logger.Warn("push auction result: marshal failed", logger.Int64("uid", uid), logger.Err(err))
		return
	}
	pc.Push(gw, uid, msgIDSCAuctionResult, body)
}

// pushMatchTimeout 若玩家在线，推 SC_MatchTimeout（同步，off-loop 调用）。
func pushMatchTimeout(pc presenceClient, uid int64) {
	gw, online := pc.Query(uid)
	if !online {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_MatchTimeout{})
	if err != nil {
		logger.Warn("push match timeout: marshal failed", logger.Int64("uid", uid), logger.Err(err))
		return
	}
	pc.Push(gw, uid, msgIDSCMatchTimeout, body)
}

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
