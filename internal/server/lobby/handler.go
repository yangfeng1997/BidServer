package lobby

import (
	corerpc "project/internal/core/rpc"
	handlerpb "project/protocol/handler"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) Ping(_ corerpc.Ctx, req *handlerpb.CS_Ping_Req, reply corerpc.Reply[*handlerpb.SC_Pong_Rsp]) {
	text := "pong"
	if req != nil && req.GetText() != "" {
		text = "pong:" + req.GetText()
	}
	reply(&handlerpb.SC_Pong_Rsp{Text: text}, nil)
}
