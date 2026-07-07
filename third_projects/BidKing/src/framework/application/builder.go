package application

import (
	"context"
	"project/src/common/logger"
	"project/src/common/serialize"
	"project/src/framework/agent"
	"project/src/framework/cluster"
	"project/src/framework/network/acceptor"
	"project/src/framework/network/handshake"
)

// Builder Application 构建器，链式 API，Build() 完成依赖注入和合法性检查。
//
// 用法：
//
//	app := application.NewBuilder().
//	    NodeID("1.1.1").
//	    NodeType("gatesvr").
//	    Frontend(":8888").
//	    Serializer("json", json.NewSerializer()).
//	    Routes(routes.Config()).
//	    Build()
type Builder struct {
	opts              []Option
	hasSerializer     bool
	isFrontend        bool
	hasRoutes         bool
	pendingValidators []handshake.Validator
}

// NewBuilder 创建 Builder
func NewBuilder() *Builder {
	return &Builder{}
}

// NodeID 设置节点 ID
func (b *Builder) NodeID(id string) *Builder {
	b.opts = append(b.opts, WithNodeID(id))
	return b
}

// NodeType 设置节点类型，如 "gatesvr"、"lobbysvr"
func (b *Builder) NodeType(t string) *Builder {
	b.opts = append(b.opts, WithNodeType(t))
	return b
}

// Frontend 标记为前端节点，addr 非空时自动创建 TCPAcceptor
func (b *Builder) Frontend(addr string) *Builder {
	b.isFrontend = true
	b.opts = append(b.opts, WithFrontend(addr))
	return b
}

// Serializer 注入序列化器（必填）
func (b *Builder) Serializer(name string, ser serialize.Serializer) *Builder {
	b.hasSerializer = true
	b.opts = append(b.opts, WithSerializer(name, ser))
	return b
}

// Cluster 注入集群实现，默认为 noopCluster
func (b *Builder) Cluster(c cluster.Cluster) *Builder {
	b.opts = append(b.opts, WithCluster(c))
	return b
}

// Routes 注入路由表，仅前端节点需要
func (b *Builder) Routes(msgRoute map[uint32]string, forward map[uint32]string, respMsgID map[uint32]uint32) *Builder {
	b.hasRoutes = true
	b.opts = append(b.opts, WithRoutes(msgRoute, forward, respMsgID))
	return b
}

// HandshakeValidator 注册握手校验函数，可多次调用叠加
func (b *Builder) HandshakeValidator(fn handshake.Validator) *Builder {
	b.pendingValidators = append(b.pendingValidators, fn)
	return b
}

// ForwardFunc 自定义 gate 转发函数，覆盖框架默认实现
func (b *Builder) ForwardFunc(fn func(context.Context, *agent.ForwardContext)) *Builder {
	b.opts = append(b.opts, func(a *Application) {
		a.customForwardFn = fn
	})
	return b
}

// Acceptor 添加额外 Acceptor（如 WSAcceptor）。
// 必须先调 Frontend()，否则 Build() 时 panic。
func (b *Builder) Acceptor(acc acceptor.Acceptor) *Builder {
	if !b.isFrontend {
		panic("Builder: Acceptor() requires Frontend() to be called first")
	}
	b.opts = append(b.opts, func(a *Application) {
		a.AddAcceptor(acc)
	})
	return b
}

// Build 执行合法性检查，完成依赖注入，返回 *Application
func (b *Builder) Build() *Application {
	if !b.hasSerializer {
		panic("Builder: Serializer() is required")
	}
	if b.isFrontend && !b.hasRoutes {
		logger.Warn("Builder: frontend node has no Routes(), client messages will be dropped")
	}

	a := New(b.opts...)

	for _, v := range b.pendingValidators {
		a.AddHandshakeValidator(v)
	}

	return a
}
