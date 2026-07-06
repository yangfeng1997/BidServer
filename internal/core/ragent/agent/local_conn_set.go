package agent

import (
	"errors"
	"sync"
)

// ErrLocalConnNotFound 表示本地连接未注册。
var ErrLocalConnNotFound = errors.New("local conn not found")

// LocalConnSet 维护本机业务进程的 UDS 连接注册表。
// 它是 RouterAgent 的 state 层组件，封装 nodeID -> UDSConn 的并发映射。
type LocalConnSet struct {
	mu    sync.RWMutex
	conns map[uint32]*UDSConn
}

// NewLocalConnSet 创建本机连接注册表。
func NewLocalConnSet() *LocalConnSet {
	return &LocalConnSet{conns: make(map[uint32]*UDSConn)}
}

// Register 注册或覆盖一个 nodeID 对应的连接。
func (s *LocalConnSet) Register(nodeID uint32, c *UDSConn) {
	s.mu.Lock()
	s.conns[nodeID] = c
	s.mu.Unlock()
}

// Get 按 nodeID 查询连接。
func (s *LocalConnSet) Get(nodeID uint32) *UDSConn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conns[nodeID]
}

// Deliver 向指定 nodeID 的本机连接投递帧。
func (s *LocalConnSet) Deliver(nodeID uint32, f Frame) error {
	c := s.Get(nodeID)
	if c == nil {
		return ErrLocalConnNotFound
	}
	return c.Send(f)
}

// Remove 移除指定连接，返回被移除的 nodeID 列表。
func (s *LocalConnSet) Remove(c *UDSConn) []uint32 {
	if c == nil {
		return nil
	}
	removed := make([]uint32, 0, 1)
	s.mu.Lock()
	for nodeID, conn := range s.conns {
		if conn == c {
			delete(s.conns, nodeID)
			removed = append(removed, nodeID)
		}
	}
	s.mu.Unlock()
	return removed
}
