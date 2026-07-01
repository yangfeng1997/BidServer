package routeragent

import (
	"sync"
	"sync/atomic"
)

type remoteSeqEntry struct {
	udsConn   *UDSConn
	origSeqID uint64
}

// 远端 seq 映射表
type RemoteSeqMap struct {
	seq  atomic.Uint64
	mu   sync.Mutex
	data map[uint64]*remoteSeqEntry
}

// 创建远端 seq 表
func NewRemoteSeqMap() *RemoteSeqMap {
	return &RemoteSeqMap{data: make(map[uint64]*remoteSeqEntry)}
}

// Alloc 分配远端 seq
func (m *RemoteSeqMap) Alloc(c *UDSConn, origSeqID uint64) uint64 {
	id := m.seq.Add(1)
	m.mu.Lock()
	m.data[id] = &remoteSeqEntry{udsConn: c, origSeqID: origSeqID}
	m.mu.Unlock()
	return id
}

// Pop 取出并删除远端 seq
func (m *RemoteSeqMap) Pop(id uint64) *remoteSeqEntry {
	m.mu.Lock()
	entry := m.data[id]
	if entry != nil {
		delete(m.data, id)
	}
	m.mu.Unlock()
	return entry
}

// 删除指定连接的全部远端 seq
func (m *RemoteSeqMap) DeleteByConn(c *UDSConn) int {
	if c == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for id, entry := range m.data {
		if entry.udsConn == c {
			delete(m.data, id)
			count++
		}
	}
	return count
}

// RemoteSeqEntry 公开版本（集成测试用）
type RemoteSeqEntry struct {
	UDSConn   *UDSConn
	OrigSeqID uint64
}

// PopPublic 公开的 Pop（集成测试用）
func (m *RemoteSeqMap) PopPublic(id uint64) *RemoteSeqEntry {
	entry := m.Pop(id)
	if entry == nil {
		return nil
	}
	return &RemoteSeqEntry{UDSConn: entry.udsConn, OrigSeqID: entry.origSeqID}
}

// PendingAdd 增减 pending 计数（集成测试用）
func (m *RemoteSeqMap) PendingAdd(delta int64) {}
