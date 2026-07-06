package routeragent

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"project/pkg/logger"
)

const (
	peerDialTimeout  = 3 * time.Second
	peerPendingLimit = 65536
)

var errPeerQueueFull = errors.New("peer pending queue full")

func peerKey(addr string, serverType uint32) string {
	return fmt.Sprintf("%s:%d", addr, serverType)
}

// PeerLink 表示一个可发送帧的 peer 连接
type PeerLink interface {
	Send(Frame) error
	Close() error
}

// PeerState 表示 peer 连接状态
type PeerState uint8

const (
	PeerDisconnected PeerState = iota
	PeerConnecting
	PeerHandshaking
	PeerConnected
)

// 远端 RA 信息
type PeerInfo struct {
	Addr      string
	State     PeerState
	Link      PeerLink
	Direction string
}

type peerOutbound struct {
	source       *UDSConn
	frame        Frame
	head         RPCWireHeader
	origSeqID    uint64
	targetNodeID uint32
	prepareRPC   bool
}

// PeerMgr 管理跨机 peer 连接
type PeerMgr struct {
	mu         sync.RWMutex
	peers      map[string]*PeerInfo
	pending    map[string][]peerOutbound
	listenAddr string
}

// 创建 peer 管理器
func NewPeerMgr() *PeerMgr {
	return &PeerMgr{peers: make(map[string]*PeerInfo), pending: make(map[string][]peerOutbound)}
}

// SetListenAddr 设置本地监听地址
type peerLinkSnapshot struct {
	link  PeerLink
	state PeerState
}

func (m *PeerMgr) SetListenAddr(addr string) { m.listenAddr = addr }

// Get 获取 peer 信息快照
func (m *PeerMgr) Get(addr string) *PeerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	peer := m.peers[addr]
	if peer == nil {
		return nil
	}
	clone := *peer
	return &clone
}

func (m *PeerMgr) getLink(addr string) peerLinkSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	peer := m.peers[addr]
	if peer == nil {
		return peerLinkSnapshot{state: PeerDisconnected}
	}
	return peerLinkSnapshot{link: peer.Link, state: peer.State}
}

// Attach 绑定 peer 连接，必要时替换旧连接并返回被替换的连接和待发送队列。
func (m *PeerMgr) Attach(addr string, link PeerLink, direction string) (old PeerLink, replaced bool, pending []peerOutbound) {
	m.mu.Lock()
	defer m.mu.Unlock()
	peer := m.peers[addr]
	if peer == nil {
		peer = &PeerInfo{Addr: addr}
		m.peers[addr] = peer
	}
	if peer.Link != nil && peer.Link != link {
		old = peer.Link
		replaced = true
	}
	peer.Link = link
	peer.State = PeerConnected
	peer.Direction = direction
	pending = m.pending[addr]
	delete(m.pending, addr)
	return old, replaced, pending
}

// SetState 设置 peer 状态
func (m *PeerMgr) SetState(addr string, state PeerState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	peer := m.peers[addr]
	if peer == nil {
		peer = &PeerInfo{Addr: addr}
		m.peers[addr] = peer
	}
	peer.State = state
}

func (m *PeerMgr) enqueue(addr string, item peerOutbound) (startDial bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	peer := m.peers[addr]
	if peer == nil {
		peer = &PeerInfo{Addr: addr}
		m.peers[addr] = peer
	}
	if peer.State == PeerConnected && peer.Link != nil {
		return false, nil
	}
	if len(m.pending[addr]) >= peerPendingLimit {
		return false, errPeerQueueFull
	}
	m.pending[addr] = append(m.pending[addr], item)
	if n := len(m.pending[addr]); n == peerPendingLimit/2 || n == peerPendingLimit*3/4 || n == peerPendingLimit-1 {
		logger.Warn("routeragent peer pending queue high", logger.String("peer_key", addr), logger.Int("pending", n), logger.Int("limit", peerPendingLimit))
	}
	if peer.State != PeerConnecting && peer.State != PeerHandshaking {
		peer.State = PeerConnecting
		return true, nil
	}
	return false, nil
}

func (m *PeerMgr) failPending(addr string) []peerOutbound {
	m.mu.Lock()
	defer m.mu.Unlock()
	pending := m.pending[addr]
	delete(m.pending, addr)
	peer := m.peers[addr]
	if peer == nil {
		peer = &PeerInfo{Addr: addr}
		m.peers[addr] = peer
	}
	if peer.State == PeerConnecting || peer.State == PeerHandshaking {
		peer.State = PeerDisconnected
	}
	return pending
}

// Disconnect 移除 peer 连接。
func (m *PeerMgr) Disconnect(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.peers, addr)
	delete(m.pending, addr)
}

// Detach 移除指定连接，避免旧连接关闭时删除已经替换的新连接。
func (m *PeerMgr) Detach(addr string, link PeerLink) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	peer := m.peers[addr]
	if peer == nil || peer.Link != link {
		return false
	}
	delete(m.peers, addr)
	return true
}

// List 返回所有 peer
func (m *PeerMgr) List() []*PeerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*PeerInfo, 0, len(m.peers))
	for _, peer := range m.peers {
		clone := *peer
		out = append(out, &clone)
	}
	return out
}
