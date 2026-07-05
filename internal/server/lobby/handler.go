package lobby

import (
	corerpc "project/internal/core/rpc"
	handlerpb "project/protocol/handler"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) ClaimReward(_ corerpc.Ctx, _ *handlerpb.CS_ClaimReward_Req, reply corerpc.Reply[*handlerpb.SC_ClaimReward_Rsp]) {
	reply(&handlerpb.SC_ClaimReward_Rsp{}, nil)
}

func (h *Handler) SyncPos(_ corerpc.Ctx, _ *handlerpb.CS_SyncPos_Ntf) {}

func (h *Handler) Ping(_ corerpc.Ctx, req *handlerpb.CS_Ping_Req, reply corerpc.Reply[*handlerpb.SC_Tong_Rsp]) {
	text := "tong"
	if req != nil && req.GetText() != "" {
		text = "tong:" + req.GetText()
	}
	reply(&handlerpb.SC_Tong_Rsp{Text: text}, nil)
}
