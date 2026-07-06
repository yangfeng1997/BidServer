package routeragent

// 路由决策类型
type RoutingMode uint8

const (
	RoutingModeAny RoutingMode = iota
	RoutingModeDirect
	RoutingModeHash
	RoutingModeBroadcast
)

// Resolver 负责节点选择
type Resolver struct{}

// 创建路由器
func NewResolver() *Resolver { return &Resolver{} }

// PickAny 选择一个可用节点
func (r *Resolver) PickAny(list []NodeInfo) (NodeInfo, bool) {
	if len(list) == 0 {
		return NodeInfo{}, false
	}
	return list[0], true
}

// 按 nodeID 精确选择
func (r *Resolver) PickDirect(list []NodeInfo, nodeID uint32) (NodeInfo, bool) {
	for _, info := range list {
		if info.NodeID == nodeID {
			return info, true
		}
	}
	return NodeInfo{}, false
}

// PickHash 按 key 选择。调用方应传入按 NodeID 排序的列表，以保证多节点路由稳定。
func (r *Resolver) PickHash(list []NodeInfo, key string) (NodeInfo, bool) {
	if len(list) == 0 {
		return NodeInfo{}, false
	}
	if len(list) == 1 {
		return list[0], true
	}
	idx := int(hashString(key) % uint32(len(list)))
	return list[idx], true
}

// 返回所有节点
func (r *Resolver) PickBroadcast(list []NodeInfo) []NodeInfo {
	out := make([]NodeInfo, len(list))
	copy(out, list)
	return out
}

func hashString(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
