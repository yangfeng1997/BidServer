package application

import (
	"project/src/common/serialize"
	"project/src/framework/cluster"
	"project/src/framework/network/acceptor"
)

// Option Application 的函数式配置选项
type Option func(*Application)

// WithFrontend 标记当前节点为前端节点（如 gatesvr），自动创建 TCPAcceptor 监听指定地址。
// 后端节点（lobbysvr、roomsvr 等）不应调用此选项。
// 若需要 WebSocket 或自定义 Acceptor，调用 AddAcceptor() 手动添加（同样需要 WithFrontend）。
func WithFrontend(listenAddr string) Option {
	return func(a *Application) {
		a.isFrontend = true
		if listenAddr != "" {
			a.acceptors = append(a.acceptors, acceptor.NewTCPAcceptor(listenAddr))
		}
	}
}

// WithRoutes 注入路由表，仅前端节点（WithFrontend）需要调用。
// 通常直接传入 gen_routes 生成的三张表：
//
//	app.WithRoutes(routes.MsgRouteTable, routes.ForwardTable, routes.RespMsgIDTable)
func WithRoutes(msgRoute map[uint32]string, forward map[uint32]string, respMsgID map[uint32]uint32) Option {
	return func(a *Application) {
		a.msgRouteTable = msgRoute
		a.forwardTable = forward
		a.respMsgIDTable = respMsgID
	}
}

// WithNodeID 设置节点 ID，默认 "node-1"
func WithNodeID(id string) Option {
	return func(a *Application) { a.nodeID = id }
}

// WithNodeType 设置节点类型，如 "gate"、"login"，默认 "default"
func WithNodeType(t string) Option {
	return func(a *Application) { a.nodeType = t }
}

// WithCluster 注入集群实现，默认为 noopCluster
func WithCluster(c cluster.Cluster) Option {
	return func(a *Application) { a.cls = c }
}

// WithSerializer 注入序列化器（必填），name 用于握手协商告知客户端（如 "json"、"protobuf"）
func WithSerializer(name string, ser serialize.Serializer) Option {
	return func(a *Application) {
		a.serializerName = name
		a.withSerializer(ser)
	}
}
