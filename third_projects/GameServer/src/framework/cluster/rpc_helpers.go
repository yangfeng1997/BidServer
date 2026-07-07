package cluster

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
)

// Call 泛型异步 RPC，done 回调直接携带具体 resp 类型。
// cluster 和 dispatcher 均从 ctx 读取，调用方无需显式传参。
//
// 初始化时注入一次：
//
//	s.ctx = cluster.WithCluster(ctx, app.Cluster())
//	s.ctx = cluster.WithDispatch(s.ctx, s.tq)
//
// 调用：
//
//	cluster.Call[*pb.LoadPlayerResp](s.ctx, nodeID, "DBHandler.loadPlayer", req,
//	    func(resp *pb.LoadPlayerResp, err error) {
//	        s.players[uid] = newPlayer(resp) // 主循环 goroutine，安全
//	    })
func Call[R proto.Message](ctx context.Context, target NodeID, route string, req proto.Message, done func(R, error)) {
	clusterFromCtx(ctx).Call(ctx, target, route, req, func(data []byte, err error) {
		dispatch(ctx, func() { done(unmarshalResp[R](data, err)) })
	})
}

// CallAny 泛型异步 RPC，随机节点
func CallAny[R proto.Message](ctx context.Context, serverTypeName string, route string, req proto.Message, done func(R, error)) {
	clusterFromCtx(ctx).CallAny(ctx, serverTypeName, route, req, func(data []byte, err error) {
		dispatch(ctx, func() { done(unmarshalResp[R](data, err)) })
	})
}

// CallSync 泛型同步 RPC，阻塞直到收到响应，直接返回具体 resp 类型。
//
// 无状态服务用法：
//
//	resp, err := cluster.CallSync[*pb.VerifyAccountResp](ctx, nodeID, "DBHandler.verify", req)
func CallSync[R proto.Message](ctx context.Context, target NodeID, route string, req proto.Message) (R, error) {
	data, err := clusterFromCtx(ctx).CallSync(ctx, target, route, req)
	return unmarshalResp[R](data, err)
}

// CallAnySync 泛型同步 RPC，随机节点
func CallAnySync[R proto.Message](ctx context.Context, serverTypeName string, route string, req proto.Message) (R, error) {
	data, err := clusterFromCtx(ctx).CallAnySync(ctx, serverTypeName, route, req)
	return unmarshalResp[R](data, err)
}

// dispatch 若 ctx 有 Dispatcher 则投递，否则直接执行
func dispatch(ctx context.Context, fn func()) {
	if d := dispatchFromCtx(ctx); d != nil {
		d.Enqueue(fn)
	} else {
		fn()
	}
}

// unmarshalResp 构造 R 实例并反序列化
func unmarshalResp[R proto.Message](data []byte, err error) (R, error) {
	var zero R
	if err != nil {
		return zero, err
	}
	var resp R
	resp = resp.ProtoReflect().New().Interface().(R)
	if len(data) > 0 {
		if err := proto.Unmarshal(data, resp); err != nil {
			return zero, fmt.Errorf("unmarshal resp failed: %w", err)
		}
	}
	return resp, nil
}
