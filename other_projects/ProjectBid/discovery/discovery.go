// Package discovery 提供服务发现接口，支持 etcd 注册中心。
package discovery

import (
	"context"

	"projectbid/server/cluster"
)

// SDListener 监听服务发现事件。
type SDListener interface {
	AddServer(*cluster.Server)
	RemoveServer(*cluster.Server)
}

// ServiceDiscovery 服务发现接口。
type ServiceDiscovery interface {
	// Register 注册当前服务到注册中心。
	Register(ctx context.Context) error
	// Deregister 从注册中心注销当前服务。
	Deregister() error
	// GetServersByType 按服务类型获取所有服务节点。
	GetServersByType(svType string) map[string]*cluster.Server
	// GetServer 按 ID 获取服务节点。
	GetServer(id string) (*cluster.Server, error)
	// AddListener 添加服务变更监听器。
	AddListener(listener SDListener)
	// Stop 停止服务发现。
	Stop() error
}
