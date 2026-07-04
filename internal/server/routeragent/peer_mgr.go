package routeragent

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"

	"project/pkg/logger"
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
	Addr      string
	State     PeerState
	Link      PeerLink
	Direction string
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

// Attach 绑定 peer 连接，必要时替换旧连接并返回被替换的连接。
func (m *PeerMgr) Attach(addr string, link PeerLink, direction string) (old PeerLink, replaced bool) {
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
	return old, replaced
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

// Disconnect 移除 peer 连接。
func (m *PeerMgr) Disconnect(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.peers, addr)
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

// 连接到远端 RA
func (m *Module) DialPeer(addr string) error {
	if addr == "" {
		logger.Warn("routeragent peer dial skipped: empty addr")
		return nil
	}
	listenAddr := m.peerMgr.listenAddr
	logger.Info("routeragent peer dial start", logger.String("peer_addr", addr), logger.String("listen_addr", listenAddr))
	m.peerMgr.SetState(addr, PeerConnecting)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		logger.Error("routeragent peer dial failed", logger.String("peer_addr", addr), logger.String("listen_addr", listenAddr), logger.Err(err))
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return err
	}
	logger.Info("routeragent peer tcp connected", logger.String("peer_addr", addr), logger.String("local_addr", conn.LocalAddr().String()), logger.String("remote_addr", conn.RemoteAddr().String()))

	// 发送握手指携带本地监听地址
	hsBuf := make([]byte, 2+len(listenAddr))
	binary.BigEndian.PutUint16(hsBuf[:2], uint16(len(listenAddr)))
	copy(hsBuf[2:], listenAddr)
	logger.Info("routeragent peer handshake send", logger.String("peer_addr", addr), logger.String("listen_addr", listenAddr))
	if _, err := conn.Write(hsBuf); err != nil {
		logger.Error("routeragent peer handshake send failed", logger.String("peer_addr", addr), logger.String("listen_addr", listenAddr), logger.Err(err))
		conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return err
	}

	// 接收对端 Handshake
	buf := make([]byte, 2)
	logger.Info("routeragent peer handshake receive start", logger.String("peer_addr", addr))
	if _, err := io.ReadFull(conn, buf); err != nil {
		logger.Error("routeragent peer handshake receive header failed", logger.String("peer_addr", addr), logger.Err(err))
		conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return err
	}
	peerAddrLen := int(binary.BigEndian.Uint16(buf))
	if peerAddrLen > 256 || peerAddrLen <= 0 {
		logger.Warn("routeragent peer handshake invalid addr length", logger.String("peer_addr", addr), logger.Int("addr_len", peerAddrLen))
		conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		return errors.New("invalid peer addr length")
	}
	peerAddrBuf := make([]byte, peerAddrLen)
	if _, err := io.ReadFull(conn, peerAddrBuf); err != nil {
		logger.Error("routeragent peer handshake receive addr failed", logger.String("peer_addr", addr), logger.Int("addr_len", peerAddrLen), logger.Err(err))
		conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return err
	}
	peerListenAddr := string(peerAddrBuf)
	logger.Info("routeragent peer handshake receive done", logger.String("peer_addr", addr), logger.String("peer_listen_addr", peerListenAddr), logger.String("listen_addr", listenAddr))

	// 包装为 PeerLink 并启动读写循环。
	pl := &tcpPeerLink{conn: conn, addr: peerListenAddr, sendCh: make(chan Frame, 64), done: make(chan struct{})}
	old, replaced := m.peerMgr.Attach(peerListenAddr, pl, "outgoing")
	if replaced {
		logger.Warn("routeragent peer replaced old connection", logger.String("direction", "outgoing"), logger.String("peer_listen_addr", peerListenAddr), logger.String("listen_addr", listenAddr))
		m.metrics.PeerDisconnectTotal.Add(1)
		_ = old.Close()
	}
	m.metrics.PeerConnectTotal.Add(1)
	logger.Info("routeragent peer connected", logger.String("direction", "outgoing"), logger.String("peer_addr", addr), logger.String("peer_listen_addr", peerListenAddr), logger.String("listen_addr", listenAddr))

	go func() {
		writeDone := make(chan struct{})
		go pl.writeLoop(writeDone)
		pl.readLoop(func(f Frame) {
			m.poster.Post(func() {
				m.handlePeerFrame(f)
			})
		})
		close(writeDone)
		if m.peerMgr.Detach(peerListenAddr, pl) {
			m.metrics.PeerDisconnectTotal.Add(1)
			logger.Warn("routeragent peer disconnected", logger.String("direction", "outgoing"), logger.String("peer_listen_addr", peerListenAddr))
		}
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
