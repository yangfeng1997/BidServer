package routeragent

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
)

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
	Addr  string
	State PeerState
	Link  PeerLink
}

// PeerMgr 管理跨机 peer 连接
type PeerMgr struct {
	mu         sync.RWMutex
	peers      map[string]*PeerInfo
	listenAddr string
}

// 创建 peer 管理器
func NewPeerMgr() *PeerMgr {
	return &PeerMgr{peers: make(map[string]*PeerInfo)}
}

// SetListenAddr 设置本地监听地址（用于握手字典序比较）
func (m *PeerMgr) SetListenAddr(addr string) { m.listenAddr = addr }

// Get 获取 peer 信息
func (m *PeerMgr) Get(addr string) *PeerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.peers[addr]
}

// Attach 绑定 peer 连接
func (m *PeerMgr) Attach(addr string, link PeerLink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	peer := m.peers[addr]
	if peer == nil {
		peer = &PeerInfo{Addr: addr}
		m.peers[addr] = peer
	}
	peer.Link = link
	peer.State = PeerConnected
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

// 移除 peer 连接
func (m *PeerMgr) Disconnect(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.peers, addr)
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

// 连接到远端 RA
func (m *Module) DialPeer(addr string) error {
	if addr == "" {
		return nil
	}
	m.peerMgr.SetState(addr, PeerConnecting)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return err
	}

	// 发送握手指携带本地监听地址
	listenAddr := m.peerMgr.listenAddr
	hsBuf := make([]byte, 2+len(listenAddr))
	binary.BigEndian.PutUint16(hsBuf[:2], uint16(len(listenAddr)))
	copy(hsBuf[2:], listenAddr)
	if _, err := conn.Write(hsBuf); err != nil {
		conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return err
	}

	// 接收对端 Handshake
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return err
	}
	peerAddrLen := int(binary.BigEndian.Uint16(buf))
	if peerAddrLen > 256 || peerAddrLen <= 0 {
		conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		return errors.New("invalid peer addr length")
	}
	peerAddrBuf := make([]byte, peerAddrLen)
	if _, err := io.ReadFull(conn, peerAddrBuf); err != nil {
		conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return err
	}
	peerListenAddr := string(peerAddrBuf)

	// 防双向重复建连
	if !m.DedupPeer(listenAddr, peerListenAddr, "outgoing") {
		conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return errors.New("dedup: connection rejected")
	}

	// 包装为 PeerLink 并启动读写循环
	pl := &tcpPeerLink{conn: conn, addr: peerListenAddr, sendCh: make(chan Frame, 64), done: make(chan struct{})}
	m.peerMgr.Attach(peerListenAddr, pl)
	m.peerMgr.SetState(peerListenAddr, PeerConnected)
	m.metrics.PeerConnectTotal.Add(1)

	go func() {
		writeDone := make(chan struct{})
		go pl.writeLoop(writeDone)
		pl.readLoop(func(f Frame) {
			m.poster.Post(func() {
				m.handlePeerFrame(f)
			})
		})
		close(writeDone)
		m.peerMgr.Disconnect(peerListenAddr)
		m.metrics.PeerDisconnectTotal.Add(1)
	}()

	return nil
}

// 转发帧到指定 peer
func (m *Module) ForwardFrame(addr string, frame Frame) error {
	peer := m.peerMgr.Get(addr)
	if peer == nil || peer.Link == nil {
		if err := m.DialPeer(addr); err != nil {
			return err
		}
		peer = m.peerMgr.Get(addr)
	}
	if peer == nil || peer.Link == nil {
		return nil
	}
	return peer.Link.Send(frame)
}

// 发送握手帧
func (m *Module) HandshakePeer(addr string, nodeID uint32) error {
	peer := m.peerMgr.Get(addr)
	if peer == nil || peer.Link == nil {
		return errors.New("peer not connected")
	}
	body := make([]byte, 4)
	binary.BigEndian.PutUint32(body, nodeID)
	return peer.Link.Send(Frame{Type: FrameHandshake, Body: body})
}

// DedupPeer 防双向重复建连
func (m *Module) DedupPeer(localAddr, remoteAddr, direction string) bool {
	if localAddr > remoteAddr {
		return direction == "outgoing"
	}
	return direction == "incoming"
}
