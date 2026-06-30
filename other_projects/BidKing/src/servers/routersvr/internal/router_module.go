package internal

import (
	"context"
	"fmt"

	matchpb "project/protocal/gen/match"
	routerpb "project/protocal/gen/router"
	"project/src/common/jumphash"
	"project/src/common/logger"
	"project/src/common/matchqueue"
	"project/src/framework/cluster"
	clusterpb "project/src/framework/cluster/pb"
	"project/src/framework/module"
)

// Discoverer 提供按类型枚举实例（*transport.NatsCluster.Discovery() 满足）
type Discoverer interface {
	ByType(serverTypeName string) []*clusterpb.NodeInfo
}

// RouterModule 无状态：按 RoutingMode 把逻辑目标解析到具体实例。
// 一致性哈希成员每次从 discovery 实时读取（discovery 是成员真相，
// 由 watch+周期对账维护），用无状态 jumphash.Pick 选择，始终一致、无缓存。
type RouterModule struct {
	module.BaseModule
	disc Discoverer
	cls  cluster.Cluster
	mq   matchqueue.MatchQueue // 匹配请求 publisher（可为 nil：不接 JetStream 的实例/单测）
}

func NewRouterModule(disc Discoverer, cls cluster.Cluster, mq matchqueue.MatchQueue) *RouterModule {
	return &RouterModule{disc: disc, cls: cls, mq: mq}
}

// PublishMatch 把匹配请求发布到 MATCH stream。
func (m *RouterModule) PublishMatch(ctx context.Context, req *matchpb.MatchRequest) error {
	if m.mq == nil {
		return fmt.Errorf("router: match queue not configured")
	}
	return m.mq.Publish(ctx, matchqueue.SubjectMatchRequest, req)
}

func (m *RouterModule) Name() string             { return "router" }
func (m *RouterModule) Cluster() cluster.Cluster { return m.cls }

func (m *RouterModule) Init() { logger.Info("router module initialized") }

// Resolve 把 {目标类型, 模式, key} 解析为具体目标 NodeID。
func (m *RouterModule) Resolve(targetType string, mode routerpb.RoutingMode, key string) (cluster.NodeID, bool) {
	switch mode {
	case routerpb.RoutingMode_ROUTING_CONSISTENT_HASH:
		nodes := m.disc.ByType(targetType)
		members := make([]string, 0, len(nodes))
		for _, n := range nodes {
			members = append(members, n.NodeId)
		}
		node, ok := jumphash.Pick(members, key)
		if !ok {
			return 0, false
		}
		id, err := cluster.ParseNodeID(node)
		return id, err == nil
	case routerpb.RoutingMode_ROUTING_DIRECT:
		id, err := cluster.ParseNodeID(key)
		return id, err == nil
	default:
		// ROUTING_ANY 等 P4（match/room）再实现
		return 0, false
	}
}
