package gatesvr

import (
	"sync/atomic"
	"time"

	"project/internal/core/conn"
)

type pendingEntry struct {
	conn     conn.Connection
	seqID    uint16
	rspCmdID uint32
	timer    *time.Timer
}

// PendingMap 维护 gate 转发后的回写表
type PendingMap struct {
	seq  atomic.Uint64
	data map[uint64]*pendingEntry
}

// NewPendingMap 创建回写表
func NewPendingMap() *PendingMap {
	return &PendingMap{data: make(map[uint64]*pendingEntry)}
}

// Alloc 分配一个新的 pending id
func (m *PendingMap) Alloc(c conn.Connection, seqID uint16, rspCmdID uint32, timeout time.Duration, onTimeout func(uint64)) uint64 {
	id := m.seq.Add(1)
	entry := &pendingEntry{conn: c, seqID: seqID, rspCmdID: rspCmdID}
	if timeout > 0 && onTimeout != nil {
		entry.timer = time.AfterFunc(timeout, func() { onTimeout(id) })
	}
	m.data[id] = entry
	return id
}

// Pop 取出并删除一个 pending
func (m *PendingMap) Pop(id uint64) *pendingEntry {
	entry := m.data[id]
	if entry == nil {
		return nil
	}
	delete(m.data, id)
	if entry.timer != nil {
		entry.timer.Stop()
	}
	return entry
}

// DeleteByConn 删除指定连接关联的所有 pending
func (m *PendingMap) DeleteByConn(c conn.Connection) int {
	if c == nil {
		return 0
	}
	count := 0
	for id, entry := range m.data {
		if entry.conn == c {
			if entry.timer != nil {
				entry.timer.Stop()
			}
			delete(m.data, id)
			count++
		}
	}
	return count
}
