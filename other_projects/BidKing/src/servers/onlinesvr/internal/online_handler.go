package internal

import (
	"context"
	"time"

	onlinepb "project/protocal/gen/online"
	"project/src/common/logger"
	"project/src/framework/cluster"
)

const kickReasonDupLogin int32 = 1

// OnlineHandler 在线目录的集群 RPC handler。
// 持 Directory（状态）+ cluster（顶号 Cast 直达旧 gateway）。
type OnlineHandler struct {
	dir *Directory
	cls cluster.Cluster
}

func NewOnlineHandler(dir *Directory, cls cluster.Cluster) *OnlineHandler {
	return &OnlineHandler{dir: dir, cls: cls}
}

// Register 注册在线；检测到跨 gateway 重复登录则直达旧 gateway 踢号。
func (h *OnlineHandler) Register(ctx context.Context, req *onlinepb.RPC_Register_Req) (*onlinepb.RPC_Register_Rsp, error) {
	old, replaced := h.dir.Register(req.Uid, req.GatewayNodeId, req.LobbyNodeId, time.Now().UnixNano())
	if replaced && old != nil {
		gw, err := cluster.ParseNodeID(old.GatewayNodeID)
		if err != nil {
			logger.Warn("online: bad old gateway nodeID",
				logger.Int64("uid", req.Uid), logger.String("nodeID", old.GatewayNodeID))
		} else if err := h.cls.Cast(ctx, gw, "GateHandler.kicksession",
			&onlinepb.RPC_KickSession_Notify{Uid: req.Uid, Reason: kickReasonDupLogin}); err != nil {
			logger.Warn("online: kick cast failed", logger.Int64("uid", req.Uid), logger.Err(err))
		}
	}
	return &onlinepb.RPC_Register_Rsp{Code: 0, KickedOld: replaced}, nil
}

// Query 定位玩家。
func (h *OnlineHandler) Query(_ context.Context, req *onlinepb.RPC_Query_Req) (*onlinepb.RPC_Query_Rsp, error) {
	e, ok := h.dir.Query(req.Uid)
	if !ok {
		return &onlinepb.RPC_Query_Rsp{Online: false}, nil
	}
	return &onlinepb.RPC_Query_Rsp{Online: true, Entry: &onlinepb.OnlineEntry{
		Uid: e.Uid, GatewayNodeId: e.GatewayNodeID, LobbyNodeId: e.LobbyNodeID,
		LoginTime: e.LoginTime, LastActive: e.LastActive,
		RoomNodeId: e.RoomNodeID, GameId: e.GameID,
	}}, nil
}

// Unregister 注销（幂等）。
func (h *OnlineHandler) Unregister(_ context.Context, req *onlinepb.RPC_Unregister_Req) (*onlinepb.RPC_Unregister_Rsp, error) {
	h.dir.Unregister(req.Uid)
	return &onlinepb.RPC_Unregister_Rsp{Code: 0}, nil
}

// Touch 刷新活跃。
func (h *OnlineHandler) Touch(_ context.Context, req *onlinepb.RPC_Touch_Req) (*onlinepb.RPC_Touch_Rsp, error) {
	ok := h.dir.Touch(req.Uid, time.Now().UnixNano())
	return &onlinepb.RPC_Touch_Rsp{Code: 0, Online: ok}, nil
}

// Bindroom 绑定 room 亲和（绝对覆盖写）；条目不在线返回 code≠0。
func (h *OnlineHandler) Bindroom(_ context.Context, req *onlinepb.RPC_BindRoom_Req) (*onlinepb.RPC_BindRoom_Rsp, error) {
	if ok := h.dir.BindRoom(req.Uid, req.RoomNodeId, req.GameId); !ok {
		logger.Warn("online: bindroom on offline uid", logger.Int64("uid", req.Uid))
		return &onlinepb.RPC_BindRoom_Rsp{Code: 1}, nil
	}
	return &onlinepb.RPC_BindRoom_Rsp{Code: 0}, nil
}

// Unbindroom 清除 room 亲和（幂等）。P4a 仅建 handler，wiring 留 P4b 结算清亲和。
func (h *OnlineHandler) Unbindroom(_ context.Context, req *onlinepb.RPC_UnbindRoom_Req) (*onlinepb.RPC_UnbindRoom_Rsp, error) {
	h.dir.UnbindRoom(req.Uid)
	return &onlinepb.RPC_UnbindRoom_Rsp{Code: 0}, nil
}
