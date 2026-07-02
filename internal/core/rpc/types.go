package rpc

import "time"

// 路由选择方式
type RoutingMode uint8

const (
	RoutingAny RoutingMode = iota
	RoutingConsistentHash
	RoutingDirect
	RoutingBroadcast
)

// RPC 目标
type Target struct {
	ServerType uint32
	Mode       RoutingMode
	Key        string
	NodeID     uint32
	Deadline   time.Duration
}

// At 直接指定目标节点
func (t Target) At(nodeID uint32) Target {
	t.Mode = RoutingDirect
	t.NodeID = nodeID
	return t
}

// ByHash 按 key 选择节点
func (t Target) ByHash(key string) Target {
	t.Mode = RoutingConsistentHash
	t.Key = key
	return t
}

// 广播到同类型所有节点
func (t Target) Broadcast() Target {
	t.Mode = RoutingBroadcast
	return t
}

// Timeout 覆盖本次调用超时时间
func (t Target) Timeout(d time.Duration) Target {
	t.Deadline = d
	return t
}

// RPC 头部信息
type Header struct {
	SeqID       uint64
	Route       string
	DeadlineMs  int64
	WaiterID    uint64
	FromNodeID  uint32
	ServerType  uint32
	RoutingMode RoutingMode
	RoutingKey  string
}

// 一次性回包句柄
type Reply[T any] func(T, error)

// 主循环投递接口
type Poster interface {
	Post(func())
}
