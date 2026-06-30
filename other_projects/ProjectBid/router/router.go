// Package router 提供服务间 RPC 路由，支持自定义路由策略和默认随机选择。
package router

import (
	"context"
	"math/rand"

	"projectbid/server/cluster"
	"projectbid/server/conn/message"
	"projectbid/server/discovery"
	"projectbid/server/route"
)

// RoutingFunc 为指定的路由选择目标服务器。
type RoutingFunc func(
	ctx context.Context,
	rt *route.Route,
	payload *message.Message,
	servers map[string]*cluster.Server,
) (*cluster.Server, error)

// Router 管理 RPC 路由策略。
type Router struct {
	serviceDiscovery discovery.ServiceDiscovery
	routesMap        map[string]RoutingFunc
	defaultRoute     RoutingFunc
}

// New 创建路由器。
func New() *Router {
	return &Router{
		routesMap:    make(map[string]RoutingFunc),
		defaultRoute: defaultRouting,
	}
}

// SetServiceDiscovery 设置服务发现实例。
func (r *Router) SetServiceDiscovery(sd discovery.ServiceDiscovery) {
	r.serviceDiscovery = sd
}

// AddRoute 为指定服务器类型添加自定义路由函数。
func (r *Router) AddRoute(serverType string, fn RoutingFunc) {
	r.routesMap[serverType] = fn
}

// Route 为 RPC 调用选择一个目标服务器。
func (r *Router) Route(
	ctx context.Context,
	rt *route.Route,
	payload *message.Message,
) (*cluster.Server, error) {
	if r.serviceDiscovery == nil {
		return nil, errServiceDiscoveryNotSet
	}

	servers := r.serviceDiscovery.GetServersByType(rt.SvType)
	if len(servers) == 0 {
		return nil, errNoServersAvailable
	}

	if fn, ok := r.routesMap[rt.SvType]; ok {
		return fn(ctx, rt, payload, servers)
	}
	return r.defaultRoute(ctx, rt, payload, servers)
}

func defaultRouting(
	_ context.Context,
	_ *route.Route,
	_ *message.Message,
	servers map[string]*cluster.Server,
) (*cluster.Server, error) {
	idx := rand.Intn(len(servers))
	for _, s := range servers {
		if idx == 0 {
			return s, nil
		}
		idx--
	}
	return nil, errNoServersAvailable
}
