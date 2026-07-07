package internal

import (
	"context"

	"google.golang.org/protobuf/proto"

	gatepb "project/protocal/gen/gate"
	lobbypb "project/protocal/gen/lobby"
	onlinepb "project/protocal/gen/online"
	"project/src/common/logger"
	"project/src/common/serialize"
	"project/src/common/serialize/protobuf"
	"project/src/framework/cluster"
	clusterpb "project/src/framework/cluster/pb"
	"project/src/framework/handler"
)

const msgIDSCKick uint32 = 1004 // 对应 gate.proto SC_Kick.msg_id，保持同步

var kickSerializer serialize.Serializer = protobuf.NewSerializer() // 推给客户端用 proto（全链路 proto）

// GateHandler 处理 gate 本地消息（登录、心跳、退出）
type GateHandler struct {
	module *GateModule
}

func NewGateHandler(m *GateModule) *GateHandler {
	return &GateHandler{module: m}
}

// Login 处理登录请求（msgID=1001，Request，有返回）。
// 转发给任一 lobbysvr 校验，成功后绑定 uid 与归属 lobby 节点。
func (h *GateHandler) Login(ctx context.Context, req *gatepb.CS_Login_Req) (*gatepb.SC_Login_Rsp, error) {
	sessionID := handler.SessionIDFromCtx(ctx)
	s, ok := h.module.Sessions().ByID(sessionID)
	if !ok {
		return &gatepb.SC_Login_Rsp{Code: -1, Message: "session not found"}, nil
	}

	// 附 ClusterSession，让 lobby 拿到本 gateway nodeID（用于在线注册定位）
	ctx = cluster.WithSession(ctx, &clusterpb.ClusterSession{
		Id:         sessionID,
		Ip:         s.IP(),
		FrontendId: h.module.NodeID(),
	})
	data, err := h.module.Cluster().CallAnySync(ctx, "lobbysvr", "LobbyHandler.login",
		&lobbypb.RPC_Login_Req{Token: req.Token, Platform: req.Platform})
	if err != nil {
		logger.Warn("login: call lobby failed", logger.Err(err))
		return &gatepb.SC_Login_Rsp{Code: -2, Message: "login service unavailable"}, nil
	}

	var lrsp lobbypb.RPC_Login_Rsp
	if err := proto.Unmarshal(data, &lrsp); err != nil {
		logger.Warn("login: decode lobby rsp failed", logger.Err(err))
		return &gatepb.SC_Login_Rsp{Code: -2, Message: "login decode error"}, nil
	}
	if lrsp.Code != 0 {
		return &gatepb.SC_Login_Rsp{Code: lrsp.Code, Message: "login rejected"}, nil
	}

	// 绑定 uid 与归属 lobby 节点（后续该连接消息转发到此 lobby）
	if err := h.module.Sessions().Bind(ctx, s, lrsp.Uid); err != nil {
		return &gatepb.SC_Login_Rsp{Code: -3, Message: err.Error()}, nil
	}
	s.BindNode("lobbysvr", lrsp.LobbyNodeId)

	logger.Info("player logged in",
		logger.Int64("uid", lrsp.Uid),
		logger.String("lobby", lrsp.LobbyNodeId),
		logger.String("platform", req.Platform))
	return &gatepb.SC_Login_Rsp{Code: 0, Uid: lrsp.Uid}, nil
}

// Heartbeat 处理客户端心跳（msgID=1003，OneWay）：转发活跃信号给绑定 lobby
func (h *GateHandler) Heartbeat(ctx context.Context, _ *gatepb.CS_Heartbeat_OneWay) {
	sessionID := handler.SessionIDFromCtx(ctx)
	s, ok := h.module.Sessions().ByID(sessionID)
	if !ok || !s.IsBound() {
		return
	}
	h.module.ForwardTouch(s)
}

// Logout 处理退出登录（msgID=1005，OneWay，无返回）
func (h *GateHandler) Logout(ctx context.Context, _ *gatepb.CS_Logout_OneWay) {
	sessionID := handler.SessionIDFromCtx(ctx)
	s, ok := h.module.Sessions().ByID(sessionID)
	if !ok {
		return
	}
	logger.Info("player logout", logger.Int64("uid", handler.UIDFromCtx(ctx)))
	h.module.Sessions().Close(s)
}

// PushToClient 处理后端推送（route="GateHandler.pushtoclient"）。
// raw []byte 入参：cluster 信封是 proto，手动 Unmarshal 取出不透明 body。
// body 已是 client 序列化器(proto) 字节，按 uid 原样透传推给客户端（gate 不转码）。
func (h *GateHandler) PushToClient(_ context.Context, raw []byte) {
	var req gatepb.RPC_PushToClient
	if err := proto.Unmarshal(raw, &req); err != nil {
		logger.Warn("gate push: unmarshal failed", logger.Err(err))
		return
	}
	s, ok := h.module.Sessions().ByUID(req.Uid)
	if !ok {
		return // 连接可能已断
	}
	if h.module.Agents() == nil {
		return
	}
	ag, ok := h.module.Agents().Load(s.ID())
	if !ok {
		return
	}
	if err := ag.Push(req.MsgId, req.Body); err != nil {
		logger.Warn("gate push: push failed", logger.Int64("uid", req.Uid), logger.Err(err))
	}
}

// KickSession 处理 onlinesvr 直达的顶号通知（route="GateHandler.kicksession"）。
// 用 raw []byte 入参手动 proto.Unmarshal（与 PushToClient 一致，最小改动）；
// gate registry 现为 proto，SC_Kick body 亦按 proto marshal 后推给客户端。
func (h *GateHandler) KickSession(_ context.Context, raw []byte) {
	var req onlinepb.RPC_KickSession_Notify
	if err := proto.Unmarshal(raw, &req); err != nil {
		logger.Warn("gate kick: unmarshal failed", logger.Err(err))
		return
	}
	s, ok := h.module.Sessions().ByUID(req.Uid)
	if !ok {
		return // 连接可能已在别处断开
	}
	if h.module.Agents() != nil {
		if ag, ok := h.module.Agents().Load(s.ID()); ok {
			if body, err := kickSerializer.Marshal(&gatepb.SC_Kick{Reason: req.Reason, Message: "logged in elsewhere"}); err == nil {
				_ = ag.Push(msgIDSCKick, body)
			}
		}
	}
	logger.Info("gate kick: closing old session", logger.Int64("uid", req.Uid))
	h.module.Sessions().Close(s)
}
