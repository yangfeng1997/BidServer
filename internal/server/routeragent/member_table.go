package routeragent

import "sync"

// NodeInfo 表示一个已注册的节点
type NodeInfo struct {
	NodeID  uint32
	RAAddr  string
	StartAt int64
}

// MemberTable 维护 nodeID 与 serverType 索引
type MemberTable struct {
	mu           sync.RWMutex
	byNodeID     map[uint32]NodeInfo
	byServerType map[uint32][]NodeInfo
}

// NewMemberTable 创建成员表
func NewMemberTable() *MemberTable {
	return &MemberTable{
		byNodeID:     make(map[uint32]NodeInfo),
		byServerType: make(map[uint32][]NodeInfo),
	}
}

// Upsert 插入或更新节点
func (m *MemberTable) Upsert(info NodeInfo, serverType uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byNodeID[info.NodeID] = info
	items := m.byServerType[serverType]
	filtered := items[:0]
	for _, it := range items {
		if it.NodeID != info.NodeID {
			filtered = append(filtered, it)
		}
	}
	m.byServerType[serverType] = append(filtered, info)
}

// GetByNodeID 按 nodeID 查询
func (m *MemberTable) GetByNodeID(id uint32) (NodeInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info, ok := m.byNodeID[id]
	return info, ok
}

// ListByServerType 获取同类型节点列表
func (m *MemberTable) ListByServerType(serverType uint32) []NodeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.byServerType[serverType]
	out := make([]NodeInfo, len(items))
	copy(out, items)
	return out
}
