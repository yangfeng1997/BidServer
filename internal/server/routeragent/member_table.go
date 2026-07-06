package routeragent

import (
	"sort"
	"sync"
)

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
	idx := sort.Search(len(items), func(i int) bool { return items[i].NodeID >= info.NodeID })
	if idx < len(items) && items[idx].NodeID == info.NodeID {
		items[idx] = info
		m.byServerType[serverType] = items
		return
	}
	items = append(items, NodeInfo{})
	copy(items[idx+1:], items[idx:])
	items[idx] = info
	m.byServerType[serverType] = items
}

// Delete 删除指定 nodeID
func (m *MemberTable) Delete(nodeID uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byNodeID, nodeID)
	for serverType, items := range m.byServerType {
		out := make([]NodeInfo, 0, len(items))
		for _, it := range items {
			if it.NodeID != nodeID {
				out = append(out, it)
			}
		}
		if len(out) == 0 {
			delete(m.byServerType, serverType)
			continue
		}
		m.byServerType[serverType] = out
	}
}

// GetByNodeID 按 nodeID 查询
func (m *MemberTable) GetByNodeID(id uint32) (NodeInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info, ok := m.byNodeID[id]
	return info, ok
}

// PickAnyByServerType 选择同类型第一个节点。
func (m *MemberTable) PickAnyByServerType(serverType uint32) (NodeInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.byServerType[serverType]
	if len(items) == 0 {
		return NodeInfo{}, false
	}
	return items[0], true
}

// PickHashByServerType 在同类型有序节点列表上按 key 选择节点。
func (m *MemberTable) PickHashByServerType(serverType uint32, key string) (NodeInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.byServerType[serverType]
	if len(items) == 0 {
		return NodeInfo{}, false
	}
	if len(items) == 1 {
		return items[0], true
	}
	return items[int(hashString(key)%uint32(len(items)))], true
}

// ListNodeIDsByServerType 获取同类型节点 ID 快照。
func (m *MemberTable) ListNodeIDsByServerType(serverType uint32) []uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.byServerType[serverType]
	out := make([]uint32, len(items))
	for i, item := range items {
		out[i] = item.NodeID
	}
	return out
}

// ListByServerType 获取同类型节点列表（返回不可变快照，调用方不可修改）
func (m *MemberTable) ListByServerType(serverType uint32) []NodeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.byServerType[serverType]
	out := make([]NodeInfo, len(items))
	copy(out, items)
	return out
}
