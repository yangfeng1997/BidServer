// Package networkentity 定义底层网络实体抽象，解耦 session 与 agent 之间的依赖。
package networkentity

import (
	"context"
	"net"
)

// NetworkEntity 是底层网络实体的接口，Session 通过它发送消息。
// Agent 实现此接口，使 Session 可以透明地向前端连接或后端远程代理发送数据。
type NetworkEntity interface {
	Push(route string, v interface{}) error
	ResponseMID(ctx context.Context, mid uint, v interface{}, isError ...bool) error
	Close() error
	RemoteAddr() net.Addr
}
