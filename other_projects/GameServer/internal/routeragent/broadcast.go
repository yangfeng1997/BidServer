package routeragent

import (
	"encoding/binary"
	"sync"
)

// 广播发送记录
type BroadcastSentRecord struct {
	WaiterID uint64
	NodeIDs  []uint32
}

// 广播回包收集器
type BroadcastWaiter struct {
	mu       sync.Mutex
	id       uint64
	nodes    map[uint32]struct{}
	received map[uint32]struct{}
}

// 创建广播收集器
func NewBroadcastWaiter(id uint64, nodes []uint32) *BroadcastWaiter {
	w := &BroadcastWaiter{
		id:       id,
		nodes:    make(map[uint32]struct{}),
		received: make(map[uint32]struct{}),
	}
	for _, n := range nodes {
		w.nodes[n] = struct{}{}
	}
	return w
}

// Mark 标记一个节点已回包
func (w *BroadcastWaiter) Mark(nodeID uint32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.received[nodeID] = struct{}{}
}

// Done 判断是否全部收齐
func (w *BroadcastWaiter) Done() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.received) == len(w.nodes)
}

// 编码广播通知
func EncodeBroadcastSent(waiterID uint64, nodeIDs []uint32) []byte {
	buf := make([]byte, 8+4+4*len(nodeIDs))
	binary.BigEndian.PutUint64(buf[:8], waiterID)
	binary.BigEndian.PutUint32(buf[8:12], uint32(len(nodeIDs)))
	for i, n := range nodeIDs {
		binary.BigEndian.PutUint32(buf[12+i*4:16+i*4], n)
	}
	return buf
}

// DecodeBroadcastSent 解码广播发出消息
func DecodeBroadcastSent(body []byte) (uint64, []uint32, error) {
	if len(body) < 12 {
		return 0, nil, nil
	}
	waiterID := binary.BigEndian.Uint64(body[:8])
	count := int(binary.BigEndian.Uint32(body[8:12]))
	if len(body) < 12+count*4 {
		return 0, nil, nil
	}
	nodeIDs := make([]uint32, count)
	for i := 0; i < count; i++ {
		nodeIDs[i] = binary.BigEndian.Uint32(body[12+i*4 : 16+i*4])
	}
	return waiterID, nodeIDs, nil
}

// RegisterWaiter 注册广播等待器
func (m *Module) RegisterWaiter(w *BroadcastWaiter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.waiters[w.id] = w
}
