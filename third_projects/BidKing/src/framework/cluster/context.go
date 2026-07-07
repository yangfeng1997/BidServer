package cluster

import (
	"context"

	"project/src/common/taskqueue"
	"project/src/framework/cluster/pb"
)

type ctxSessionKey struct{}
type ctxDispatchKey struct{}
type ctxClusterKey struct{}
type ctxTraceIDKey struct{}

// ctxGet 泛型辅助，从 ctx 取指定类型的值，不存在时返回零值
func ctxGet[T any](ctx context.Context, key any) T {
	v, _ := ctx.Value(key).(T)
	return v
}

// WithSession 把 ClusterSession 注入 ctx，Call/Cast 系列自动透传
func WithSession(ctx context.Context, sess *pb.ClusterSession) context.Context {
	return context.WithValue(ctx, ctxSessionKey{}, sess)
}

// SessionFromCtx 从 ctx 取 ClusterSession，不存在时返回 nil
func SessionFromCtx(ctx context.Context) *pb.ClusterSession {
	return ctxGet[*pb.ClusterSession](ctx, ctxSessionKey{})
}

// 取集群 session 的 UID/ID 直接用 SessionFromCtx(ctx).Uid / .Id，
// 不再单独提供 UIDFromCtx/SessionIDFromCtx，避免与 handler 包的同名工具混淆
// （handler 那套源自客户端 Session，本包源自 pb.ClusterSession，数据源不同）。

// WithTraceID 把链路追踪 ID 注入 ctx
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, ctxTraceIDKey{}, traceID)
}

// TraceIDFromCtx 从 ctx 取链路追踪 ID，不存在时返回空字符串
func TraceIDFromCtx(ctx context.Context) string {
	return ctxGet[string](ctx, ctxTraceIDKey{})
}

// WithCluster 把 Cluster 注入 ctx
func WithCluster(ctx context.Context, c Cluster) context.Context {
	return context.WithValue(ctx, ctxClusterKey{}, c)
}

// clusterFromCtx 从 ctx 取 Cluster，不存在时 panic
func clusterFromCtx(ctx context.Context) Cluster {
	c := ctxGet[Cluster](ctx, ctxClusterKey{})
	if c == nil {
		panic("cluster not found in ctx, call cluster.WithCluster first")
	}
	return c
}

// WithDispatch 把 Dispatcher 注入 ctx
func WithDispatch(ctx context.Context, d taskqueue.Dispatcher) context.Context {
	return context.WithValue(ctx, ctxDispatchKey{}, d)
}

// dispatchFromCtx 从 ctx 取 Dispatcher，不存在时返回 nil
func dispatchFromCtx(ctx context.Context) taskqueue.Dispatcher {
	return ctxGet[taskqueue.Dispatcher](ctx, ctxDispatchKey{})
}

type ctxReplierKey struct{}

// WithReplier 把 Replier 注入 ctx，供延迟回包的 handler 在主循环 continuation 中取用
func WithReplier(ctx context.Context, r Replier) context.Context {
	return context.WithValue(ctx, ctxReplierKey{}, r)
}

// ReplierFromCtx 从 ctx 取 Replier，不存在时返回 nil
func ReplierFromCtx(ctx context.Context) Replier {
	return ctxGet[Replier](ctx, ctxReplierKey{})
}
