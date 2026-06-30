package cluster

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
)

// MessageHandler 处理收到的集群 RPC 消息，由框架层自动注入，业务层无需关心
type MessageHandler func(ctx context.Context, msg []byte, route string) ([]byte, error)

// HandlerSetter 可接收 MessageHandler 注入的 cluster 实现此接口
type HandlerSetter interface {
	SetHandler(h MessageHandler)
}

// Cluster 集群通信接口
// 默认 Call/CallAny 为异步（不阻塞），适合帧驱动服务
// CallSync/CallAnySync 为同步阻塞，适合初始化、无状态服务等非帧驱动场景
type Cluster interface {
	Name() string
	Init() error
	Stop() error

	// Call 指定节点，异步有返回，req 为 proto.Message
	Call(ctx context.Context, target NodeID, route string, req proto.Message, done func([]byte, error))

	// CallRaw 指定节点，异步有返回，req 为已序列化的 []byte（转发场景用）
	CallRaw(ctx context.Context, target NodeID, route string, data []byte, done func([]byte, error))

	// CallRawSync 指定节点，同步有返回，data 为已序列化 []byte（router 转发用）
	CallRawSync(ctx context.Context, target NodeID, route string, data []byte) ([]byte, error)

	// CallSync 指定节点，同步有返回
	CallSync(ctx context.Context, target NodeID, route string, req proto.Message) ([]byte, error)

	// Cast 指定节点，无返回，req 为 proto.Message
	Cast(ctx context.Context, target NodeID, route string, msg proto.Message) error

	// CastRaw 指定节点，无返回，msg 为已序列化的 []byte（转发场景用）
	CastRaw(ctx context.Context, target NodeID, route string, data []byte) error

	// CallAny 随机节点，异步有返回
	CallAny(ctx context.Context, serverTypeName string, route string, req proto.Message, done func([]byte, error))

	// CallAnyRaw 随机节点，异步有返回，req 为已序列化的 []byte
	CallAnyRaw(ctx context.Context, serverTypeName string, route string, data []byte, done func([]byte, error))

	// CallAnySync 随机节点，同步有返回
	CallAnySync(ctx context.Context, serverTypeName string, route string, req proto.Message) ([]byte, error)

	// CastAny 随机节点，无返回
	CastAny(ctx context.Context, serverTypeName string, route string, msg proto.Message) error

	// CastAnyRaw 随机节点，无返回，msg 为已序列化的 []byte
	CastAnyRaw(ctx context.Context, serverTypeName string, route string, data []byte) error

	// Broadcast 广播所有该类型节点，无返回，失败只打日志
	Broadcast(ctx context.Context, serverTypeName string, route string, msg proto.Message)
}

// ErrDeferredReply 由 handler 返回，表示"我将经 Replier 异步回包"，
// 传输层据此跳过自动回包。仅适用于 NATS 请求-应答路径（非本地短路）。
var ErrDeferredReply = errors.New("cluster: reply deferred by handler")

// Replier 主循环发起的异步回包句柄，由传输层注入 ctx。
// data 为已序列化的响应体；err 非 nil 时回错误响应。线程安全（NATS publish 线程安全）。
type Replier interface {
	Reply(data []byte, err error)
}
