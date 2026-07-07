// Package routerclient 提供 lobby 侧「经 router 调用微服务」的统一姿势：
// 把 {目标类型, 路由模式, 路由 key, 真实 route, 真实请求} 封进转发信封，
// 经 CallAny 发到任一 routersvr，由 router 解析目标实例转发并回传响应。
package routerclient

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	routerpb "project/protocal/gen/router"
	"project/src/framework/cluster"
)

const (
	routerServerType = "routersvr"
	forwardRoute     = "RouterHandler.forward"
)

// CallViaSync 经 router 同步调用某类微服务的某个 route，返回具体响应类型 R。
//   - targetType: 目标服务类型名，如 "onlinesvr"
//   - mode/key:   路由模式与 key（CONSISTENT_HASH 时 key 为 uid 串）
//   - innerRoute: 真实业务 route，如 "OnlineHandler.register"
func CallViaSync[R proto.Message](
	ctx context.Context, cls cluster.Cluster,
	targetType string, mode routerpb.RoutingMode, key, innerRoute string,
	req proto.Message,
) (R, error) {
	var zero R
	inner, err := proto.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("routerclient: marshal inner req: %w", err)
	}
	env := &routerpb.RPC_RouterForward_Req{
		RoutingMode: mode,
		TargetType:  targetType,
		RoutingKey:  key,
		InnerRoute:  innerRoute,
		InnerData:   inner,
	}
	data, err := cls.CallAnySync(ctx, routerServerType, forwardRoute, env)
	if err != nil {
		return zero, fmt.Errorf("routerclient: call router: %w", err)
	}
	var rsp routerpb.RPC_RouterForward_Rsp
	if err := proto.Unmarshal(data, &rsp); err != nil {
		return zero, fmt.Errorf("routerclient: unmarshal forward rsp: %w", err)
	}
	if rsp.Code != 0 {
		return zero, fmt.Errorf("routerclient: router forward failed code=%d: %s", rsp.Code, rsp.ErrMsg)
	}
	out := newProto[R]()
	if len(rsp.InnerData) > 0 {
		if err := proto.Unmarshal(rsp.InnerData, out); err != nil {
			return zero, fmt.Errorf("routerclient: unmarshal inner rsp: %w", err)
		}
	}
	return out, nil
}

// newProto 构造 R 的新实例（R 为指针类型的 proto.Message）
func newProto[R proto.Message]() R {
	var r R
	return r.ProtoReflect().New().Interface().(R)
}
