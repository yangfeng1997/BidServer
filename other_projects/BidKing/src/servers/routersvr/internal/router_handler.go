package internal

import (
	"context"

	matchpb "project/protocal/gen/match"
	routerpb "project/protocal/gen/router"
	"project/src/common/logger"
)

// RouterHandler 唯一转发 handler（route="RouterHandler.forward"）。
// 解析目标实例 → 同步 relay（CallRawSync）→ 响应原样回传。
// 因 routersvr 开启 asyncDispatch，每条转发各跑 goroutine，互不串行。
type RouterHandler struct {
	module *RouterModule
}

func NewRouterHandler(m *RouterModule) *RouterHandler {
	return &RouterHandler{module: m}
}

func (h *RouterHandler) Forward(ctx context.Context, req *routerpb.RPC_RouterForward_Req) (*routerpb.RPC_RouterForward_Rsp, error) {
	target, ok := h.module.Resolve(req.TargetType, req.RoutingMode, req.RoutingKey)
	if !ok {
		logger.Warn("router: no target",
			logger.String("type", req.TargetType), logger.String("key", req.RoutingKey))
		return &routerpb.RPC_RouterForward_Rsp{Code: 1, ErrMsg: "no target for " + req.TargetType}, nil
	}
	respData, err := h.module.Cluster().CallRawSync(ctx, target, req.InnerRoute, req.InnerData)
	if err != nil {
		logger.Warn("router: forward failed",
			logger.String("target", target.String()), logger.String("route", req.InnerRoute), logger.Err(err))
		return &routerpb.RPC_RouterForward_Rsp{Code: 2, ErrMsg: err.Error()}, nil
	}
	return &routerpb.RPC_RouterForward_Rsp{Code: 0, InnerData: respData}, nil
}

// Publishmatch 把匹配请求写入 JetStream MATCH stream（route="RouterHandler.publishmatch"）。
func (h *RouterHandler) Publishmatch(ctx context.Context, req *matchpb.MatchRequest) (*matchpb.RPC_PublishMatch_Rsp, error) {
	if err := h.module.PublishMatch(ctx, req); err != nil {
		logger.Warn("router publishmatch failed", logger.Int64("uid", req.Uid), logger.Err(err))
		return &matchpb.RPC_PublishMatch_Rsp{Code: 1}, nil
	}
	return &matchpb.RPC_PublishMatch_Rsp{Code: 0}, nil
}
