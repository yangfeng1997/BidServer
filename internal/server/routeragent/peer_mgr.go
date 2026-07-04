package routeragent

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"project/internal/core/errcode"
	"project/pkg/logger"
)

const (
	peerDialTimeout  = 3 * time.Second
	peerPendingLimit = 8192
)

var errPeerQueueFull = errors.New("peer pending queue full")

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

func (m *Module) sendPeerOrQueue(addr string, item peerOutbound) error {
	if addr == "" {
		m.failOutbound(item, errcode.ERR_NO_ROUTE)
		return errors.New("peer addr is empty")
	}
	if snap := m.peerMgr.getLink(addr); snap.state == PeerConnected && snap.link != nil {
		if err := m.sendPeerOutbound(snap.link, item); err == nil {
			return nil
		} else {
			logger.Warn("routeragent peer send failed, queue and reconnect", logger.String("peer_addr", addr), logger.Err(err))
		}
	}
	startDial, err := m.peerMgr.enqueue(addr, item)
	if err != nil {
		logger.Error("routeragent peer enqueue failed", logger.String("peer_addr", addr), logger.Err(err))
		m.failOutbound(item, errcode.ERR_INTERNAL)
		return err
	}
	if startDial {
		m.startDialPeer(addr)
	}
	return nil
}

func (m *Module) sendPeerOutbound(link PeerLink, item peerOutbound) error {
	frame := item.frame
	if item.prepareRPC {
		head := item.head
		if frame.Type == FrameRpcRequest {
			remoteSeq := m.remoteSeq.Alloc(item.source, item.origSeqID)
			head.SeqID = remoteSeq
		}
		if item.targetNodeID != 0 {
			head.DestNodeID = item.targetNodeID
			head.RoutingMode = uint8(RoutingModeDirect)
			head.RoutingKey = fmt.Sprintf("%d", item.targetNodeID)
		}
		frame.Header = EncodeRPCWireHeader(head)
		if err := link.Send(frame); err != nil {
			if frame.Type == FrameRpcRequest {
				m.remoteSeq.Pop(head.SeqID)
			}
			m.failOutbound(item, errcode.ERR_INTERNAL)
			return err
		}
		return nil
	}
	if err := link.Send(frame); err != nil {
		m.failOutbound(item, errcode.ERR_INTERNAL)
		return err
	}
	return nil
}

func (m *Module) failOutbound(item peerOutbound, code errcode.ErrCode) {
	if item.frame.Type != FrameRpcRequest || item.source == nil || item.origSeqID == 0 {
		return
	}
	head := item.head
	head.SeqID = item.origSeqID
	head.ErrCode = uint32(code)
	head.SrcNodeID = 0
	head.DestNodeID = item.head.SrcNodeID
	_ = item.source.Send(Frame{Type: FrameRpcResponse, Header: EncodeRPCWireHeader(head)})
}

func (m *Module) startDialPeer(addr string) {
	logger.Info("routeragent peer async dial scheduled", logger.String("peer_addr", addr))
	go func() {
		pl, peerListenAddr, err := m.dialPeerConn(addr)
		m.post(func() {
			if err != nil {
				pending := m.peerMgr.failPending(addr)
				for _, item := range pending {
					m.failOutbound(item, errcode.ERR_INTERNAL)
				}
				return
			}
			old, replaced, pending := m.peerMgr.Attach(peerListenAddr, pl, "outgoing")
			if replaced {
				logger.Warn("routeragent peer replaced old connection", logger.String("direction", "outgoing"), logger.String("peer_listen_addr", peerListenAddr), logger.String("listen_addr", m.peerMgr.listenAddr))
				m.metrics.PeerDisconnectTotal.Add(1)
				_ = old.Close()
			}
			m.metrics.PeerConnectTotal.Add(1)
			logger.Info("routeragent peer connected", logger.String("direction", "outgoing"), logger.String("peer_addr", addr), logger.String("peer_listen_addr", peerListenAddr), logger.String("listen_addr", m.peerMgr.listenAddr), logger.Int("pending", len(pending)))
			m.startPeerLoops(pl, peerListenAddr, "outgoing", addr)
			m.flushPeerPending(pl, pending)
		})
	}()
}

// 连接到远端 RA。保留给集成测试和手工调用；主路由路径使用异步 sendPeerOrQueue。
func (m *Module) DialPeer(addr string) error {
	pl, peerListenAddr, err := m.dialPeerConn(addr)
	if err != nil {
		return err
	}
	old, replaced, pending := m.peerMgr.Attach(peerListenAddr, pl, "outgoing")
	if replaced {
		logger.Warn("routeragent peer replaced old connection", logger.String("direction", "outgoing"), logger.String("peer_listen_addr", peerListenAddr), logger.String("listen_addr", m.peerMgr.listenAddr))
		m.metrics.PeerDisconnectTotal.Add(1)
		_ = old.Close()
	}
	m.metrics.PeerConnectTotal.Add(1)
	logger.Info("routeragent peer connected", logger.String("direction", "outgoing"), logger.String("peer_addr", addr), logger.String("peer_listen_addr", peerListenAddr), logger.String("listen_addr", m.peerMgr.listenAddr))
	m.startPeerLoops(pl, peerListenAddr, "outgoing", addr)
	m.flushPeerPending(pl, pending)
	return nil
}

func (m *Module) dialPeerConn(addr string) (*tcpPeerLink, string, error) {
	if addr == "" {
		logger.Warn("routeragent peer dial skipped: empty addr")
		return nil, "", errors.New("peer addr is empty")
	}
	listenAddr := m.peerMgr.listenAddr
	logger.Info("routeragent peer dial start", logger.String("peer_addr", addr), logger.String("listen_addr", listenAddr))
	m.peerMgr.SetState(addr, PeerConnecting)
	dialer := net.Dialer{Timeout: peerDialTimeout}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		logger.Error("routeragent peer dial failed", logger.String("peer_addr", addr), logger.String("listen_addr", listenAddr), logger.Err(err))
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return nil, "", err
	}
	_ = conn.SetDeadline(time.Now().Add(peerDialTimeout))
	logger.Info("routeragent peer tcp connected", logger.String("peer_addr", addr), logger.String("local_addr", conn.LocalAddr().String()), logger.String("remote_addr", conn.RemoteAddr().String()))

	hsBuf := make([]byte, 2+len(listenAddr))
	binary.BigEndian.PutUint16(hsBuf[:2], uint16(len(listenAddr)))
	copy(hsBuf[2:], listenAddr)
	logger.Info("routeragent peer handshake send", logger.String("peer_addr", addr), logger.String("listen_addr", listenAddr))
	if _, err := conn.Write(hsBuf); err != nil {
		logger.Error("routeragent peer handshake send failed", logger.String("peer_addr", addr), logger.String("listen_addr", listenAddr), logger.Err(err))
		_ = conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return nil, "", err
	}

	buf := make([]byte, 2)
	logger.Info("routeragent peer handshake receive start", logger.String("peer_addr", addr))
	if _, err := io.ReadFull(conn, buf); err != nil {
		logger.Error("routeragent peer handshake receive header failed", logger.String("peer_addr", addr), logger.Err(err))
		_ = conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return nil, "", err
	}
	peerAddrLen := int(binary.BigEndian.Uint16(buf))
	if peerAddrLen > 256 || peerAddrLen <= 0 {
		logger.Warn("routeragent peer handshake invalid addr length", logger.String("peer_addr", addr), logger.Int("addr_len", peerAddrLen))
		_ = conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		return nil, "", errors.New("invalid peer addr length")
	}
	peerAddrBuf := make([]byte, peerAddrLen)
	if _, err := io.ReadFull(conn, peerAddrBuf); err != nil {
		logger.Error("routeragent peer handshake receive addr failed", logger.String("peer_addr", addr), logger.Int("addr_len", peerAddrLen), logger.Err(err))
		_ = conn.Close()
		m.peerMgr.SetState(addr, PeerDisconnected)
		m.metrics.PeerConnectFailTotal.Add(1)
		return nil, "", err
	}
	_ = conn.SetDeadline(time.Time{})
	peerListenAddr := string(peerAddrBuf)
	logger.Info("routeragent peer handshake receive done", logger.String("peer_addr", addr), logger.String("peer_listen_addr", peerListenAddr), logger.String("listen_addr", listenAddr))
	pl := &tcpPeerLink{conn: conn, addr: peerListenAddr, sendCh: make(chan Frame, 64), done: make(chan struct{})}
	return pl, peerListenAddr, nil
}

func (m *Module) flushPeerPending(link PeerLink, pending []peerOutbound) {
	for _, item := range pending {
		if err := m.sendPeerOutbound(link, item); err != nil {
			logger.Error("routeragent peer flush pending failed", logger.Err(err))
		}
	}
}

func (m *Module) startPeerLoops(pl *tcpPeerLink, peerListenAddr string, direction string, remoteAddr string) {
	go func() {
		writeDone := make(chan struct{})
		go pl.writeLoop(writeDone)
		pl.readLoop(func(f Frame) {
			m.post(func() {
				m.handlePeerFrame(f)
			})
		})
		close(writeDone)
		if m.peerMgr.Detach(peerListenAddr, pl) {
			m.metrics.PeerDisconnectTotal.Add(1)
			logger.Warn("routeragent peer disconnected", logger.String("direction", direction), logger.String("peer_listen_addr", peerListenAddr), logger.String("remote_addr", remoteAddr))
		}
	}()
}

func (m *Module) post(fn func()) {
	if m.poster != nil {
		m.poster.Post(fn)
		return
	}
	fn()
}

// 转发帧到指定 peer
func (m *Module) ForwardFrame(addr string, frame Frame) error {
	return m.sendPeerOrQueue(addr, peerOutbound{frame: frame})
}

// 发送握手帧
func (m *Module) HandshakePeer(addr string, nodeID uint32) error {
	body := make([]byte, 4)
	binary.BigEndian.PutUint32(body, nodeID)
	return m.sendPeerOrQueue(addr, peerOutbound{frame: Frame{Type: FrameHandshake, Body: body}})
}
