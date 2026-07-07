package routeragent

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"project/internal/core/app"
	"project/internal/core/nodeid"
)

const defaultSockPath = "/run/routeragent/ra.sock"

// routeragent 业务模块
type Module struct {
	app.BaseModule
	poster      app.Poster
	ready       *app.Ready
	sockPath    string
	memberTable *MemberTable
	peerMgr     *PeerMgr
	resolver    *Resolver
	keepalive   *KeepAlive
	udsServer   *UDSServer
	tcpServer   *TCPServer
	listenAddr  string
	stopCh      chan struct{}

	connMu     sync.RWMutex
	localConns map[uint32]*UDSConn

	mu        sync.Mutex
	waiters   map[uint64]*BroadcastWaiter
	waiterSeq atomic.Uint64
	remoteSeq *RemoteSeqMap
	metrics   *Metrics
}

// NewModule 创建 routeragent 模块
func NewModule() *Module {
	return &Module{
		ready:       app.NewReady(),
		sockPath:    defaultSockPath,
		memberTable: NewMemberTable(),
		peerMgr:     NewPeerMgr(),
		resolver:    NewResolver(),
		keepalive:   NewKeepAlive(5*time.Second, 10*time.Second),
		stopCh:      make(chan struct{}),
		localConns:  make(map[uint32]*UDSConn),
		waiters:     make(map[uint64]*BroadcastWaiter),
		remoteSeq:   NewRemoteSeqMap(),
		metrics:     NewMetrics(),
	}
}

func (m *Module) Init(a *app.App) error {
	m.poster = a
	return nil
}

// AfterInit 启动 routeragent 子组件
func (m *Module) AfterInit() error {
	m.udsServer = NewUDSServer(m.sockPath, m.handleConn)
	if err := m.udsServer.Listen(); err != nil {
		return err
	}
	go m.udsServer.Serve(m.stopCh)
	go m.keepalive.Run(m.stopCh)
	if m.listenAddr != "" {
		m.peerMgr.SetListenAddr(m.listenAddr)
		m.tcpServer = NewTCPServer(m.listenAddr, m.listenAddr, m.handleIncomingPeer)
		if err := m.tcpServer.Listen(); err == nil {
			go m.tcpServer.Serve(m.stopCh)
		}
	}
	m.ready.Done()
	return nil
}

// 等待首次就绪
func (m *Module) WaitReady(ctx context.Context) error {
	return m.ready.WaitReady(ctx)
}

// BeforeStop 停止后台组件
func (m *Module) BeforeStop() {
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
	if m.udsServer != nil {
		_ = m.udsServer.Close()
		if m.tcpServer != nil {
			_ = m.tcpServer.Close()
		}
	}
}

func (m *Module) Fini() {}

func (m *Module) handleConn(c *UDSConn) {
	defer c.Close()
	for frame := range c.Recv() {
		frame := frame
		m.poster.Post(func() {
			m.handleFrame(c, frame)
		})
	}
	m.removeConn(c)
}

func (m *Module) handleFrame(c *UDSConn, frame Frame) {
	switch frame.Type {
	case FrameHandshake:
		if len(frame.Body) < 4 {
			return
		}
		nodeID := binary.BigEndian.Uint32(frame.Body[:4])
		_, serverType, _ := nodeid.Decode(nodeID)
		m.memberTable.Upsert(NodeInfo{
			NodeID:  nodeID,
			RAAddr:  c.RemoteAddr(),
			StartAt: time.Now().Unix(),
		}, serverType)
		m.registerConn(nodeID, c)
		_ = c.Send(Frame{Type: FrameHandshakeAck, Body: []byte{1}})
	case FrameHeartbeat:
		_ = c.Send(Frame{Type: FrameHeartbeat, Body: nil})
	case FrameRpcRequest, FrameRpcNotify, FrameRpcResponse:
		m.routeFrame(c, frame)
	case FrameBroadcastSent:
		m.handleBroadcastSent(frame.Body)
	}
}

func (m *Module) routeFrame(c *UDSConn, frame Frame) {
	head, err := DecodeRPCWireHeader(frame.Header)
	if err != nil || len(frame.Header) == 0 {
		m.routeLegacyFrame(c, frame)
		return
	}

	switch frame.Type {
	case FrameRpcResponse:
		if head.FromNodeID != 0 {
			_ = m.sendToNode(head.FromNodeID, frame)
			return
		}
		entry := m.remoteSeq.Pop(head.SeqID)
		if entry == nil || entry.udsConn == nil {
			return
		}
		head.SeqID = entry.origSeqID
		head.FromNodeID = 0
		encoded := EncodeRPCWireHeader(head)
		_ = entry.udsConn.Send(Frame{Type: FrameRpcResponse, Header: encoded, Body: frame.Body})
	case FrameRpcRequest, FrameRpcNotify:
		m.metrics.ForwardTotal.Add(1)
		m.forwardRPC(c, frame, head)
	}
}

func (m *Module) forwardRPC(c *UDSConn, frame Frame, head RPCWireHeader) {
	m.metrics.ForwardTotal.Add(1)
	targets := m.pickTargets(head)
	// 保存原始调用者 seq，避免广播循环覆盖
	origSeqID := head.SeqID
	if len(targets) == 0 {
		return
	}
	for _, nodeID := range targets {
		info, ok := m.memberTable.GetByNodeID(nodeID)
		if !ok {
			continue
		}
		if info.RAAddr == c.RemoteAddr() {
			local := m.localConn(nodeID)
			if local != nil {
				_ = local.Send(frame)
			}
			continue
		}
		peer := m.peerMgr.Get(info.RAAddr)
		if peer == nil || peer.Link == nil {
			if err := m.DialPeer(info.RAAddr); err != nil {
				continue
			}
			peer = m.peerMgr.Get(info.RAAddr)
		}
		if peer == nil || peer.Link == nil {
			continue
		}
		remoteSeq := m.remoteSeq.Alloc(c, origSeqID)
		head.SeqID = remoteSeq
		head.FromNodeID = nodeID
		if frame.Type == FrameRpcRequest {
			head.RoutingMode = uint8(RoutingModeDirect)
			head.RoutingKey = fmt.Sprintf("%d", nodeID)
		}
		encoded := EncodeRPCWireHeader(head)
		_ = peer.Link.Send(Frame{Type: frame.Type, Header: encoded, Body: frame.Body})
	}
}

func (m *Module) pickTargets(head RPCWireHeader) []uint32 {
	switch RoutingMode(head.RoutingMode) {
	case RoutingModeDirect:
		nodeID, err := parseNodeIDKey(head.RoutingKey)
		if err != nil {
			return nil
		}
		return []uint32{nodeID}
	case RoutingModeHash:
		list := m.memberTable.ListByServerType(head.ServerType)
		node, ok := m.resolver.PickHash(list, head.RoutingKey)
		if !ok {
			return nil
		}
		return []uint32{node.NodeID}
	case RoutingModeBroadcast:
		list := m.memberTable.ListByServerType(head.ServerType)
		nodes := m.resolver.PickBroadcast(list)
		out := make([]uint32, 0, len(nodes))
		for _, node := range nodes {
			out = append(out, node.NodeID)
		}
		return out
	default:
		list := m.memberTable.ListByServerType(head.ServerType)
		node, ok := m.resolver.PickAny(list)
		if !ok {
			return nil
		}
		return []uint32{node.NodeID}
	}
}

func (m *Module) routeLegacyFrame(c *UDSConn, frame Frame) {
	nodeID, payload, err := DecodeRouteBody(frame.Body)
	if err != nil {
		return
	}
	info, ok := m.memberTable.GetByNodeID(nodeID)
	if !ok {
		return
	}
	if info.RAAddr == c.RemoteAddr() {
		return
	}
	peer := m.peerMgr.Get(info.RAAddr)
	if peer == nil || peer.Link == nil {
		if err := m.DialPeer(info.RAAddr); err != nil {
			return
		}
		peer = m.peerMgr.Get(info.RAAddr)
	}
	if peer == nil || peer.Link == nil {
		return
	}
	_ = peer.Link.Send(Frame{Type: frame.Type, Body: EncodeRouteBody(nodeID, payload)})
}

func (m *Module) sendToNode(nodeID uint32, frame Frame) error {
	if local := m.localConn(nodeID); local != nil {
		return local.Send(frame)
	}
	info, ok := m.memberTable.GetByNodeID(nodeID)
	if !ok {
		return errors.New("node not found")
	}
	peer := m.peerMgr.Get(info.RAAddr)
	if peer == nil || peer.Link == nil {
		if err := m.DialPeer(info.RAAddr); err != nil {
			return err
		}
		peer = m.peerMgr.Get(info.RAAddr)
	}
	if peer == nil || peer.Link == nil {
		return errors.New("peer not connected")
	}
	return peer.Link.Send(frame)
}

func (m *Module) registerConn(nodeID uint32, c *UDSConn) {
	m.connMu.Lock()
	m.localConns[nodeID] = c
	m.connMu.Unlock()
}

func (m *Module) removeConn(c *UDSConn) {
	m.metrics.PeerDisconnectTotal.Add(1)
	m.connMu.Lock()
	for nodeID, conn := range m.localConns {
		if conn == c {
			delete(m.localConns, nodeID)
		}
	}
	m.connMu.Unlock()
	m.remoteSeq.DeleteByConn(c)
}

func (m *Module) localConn(nodeID uint32) *UDSConn {
	m.connMu.RLock()
	defer m.connMu.RUnlock()
	return m.localConns[nodeID]
}

func parseNodeIDKey(key string) (uint32, error) {
	if key == "" {
		return 0, errors.New("empty node id key")
	}
	v, err := strconv.ParseUint(key, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid node id %q: %w", key, err)
	}
	return uint32(v), nil
}

func (m *Module) MemberTable() *MemberTable { return m.memberTable }

func (m *Module) PeerMgr() *PeerMgr { return m.peerMgr }

func (m *Module) Resolver() *Resolver { return m.resolver }

func (m *Module) KeepAlive() *KeepAlive { return m.keepalive }

func (m *Module) handleBroadcastSent(body []byte) {
	waiterID, nodeIDs, err := DecodeBroadcastSent(body)
	if err != nil {
		return
	}
	m.mu.Lock()
	waiter := m.waiters[waiterID]
	m.mu.Unlock()
	if waiter == nil {
		return
	}
	for _, nodeID := range nodeIDs {
		waiter.Mark(nodeID)
	}
	if waiter.Done() {
		m.mu.Lock()
		delete(m.waiters, waiterID)
		m.mu.Unlock()
	}
}

// Broadcast 向同类型所有节点广播
func (m *Module) Broadcast(serverType uint32, payload []byte) BroadcastSentRecord {
	list := m.memberTable.ListByServerType(serverType)
	if len(list) == 0 {
		return BroadcastSentRecord{}
	}
	waiterID := m.waiterSeq.Add(1)
	nodeIDs := make([]uint32, 0, len(list))
	for _, info := range list {
		nodeIDs = append(nodeIDs, info.NodeID)
		_ = m.sendToNode(info.NodeID, Frame{Type: FrameRpcNotify, Body: EncodeRouteBody(info.NodeID, payload)})
	}
	m.RegisterWaiter(NewBroadcastWaiter(waiterID, nodeIDs))
	return BroadcastSentRecord{WaiterID: waiterID, NodeIDs: nodeIDs}
}

// 返回模块状态
func (m *Module) DebugString() string {
	return fmt.Sprintf("routeragent(sock=%s)", m.sockPath)
}

// RegisterConn 注册连接（集成测试用）
func (m *Module) RegisterConn(nodeID uint32, c *UDSConn) {
	m.registerConn(nodeID, c)
}

// RouteFrame 路由帧（集成测试用）
func (m *Module) RouteFrame(c *UDSConn, frame Frame) {
	m.routeFrame(c, frame)
}

// PosterFunc 将 func(func()) 适配为 app.Poster
type PosterFunc func(func())

func (f PosterFunc) Post(fn func()) { f(fn) }

// NewModuleForTest 创建用于测试的模块
func NewModuleForTest(p func(func())) *Module {
	m := NewModule()
	m.poster = PosterFunc(p)
	return m
}

// ListenAddr 公开（集成测试用）
func (m *Module) ListenAddr() string { return m.listenAddr }

// SetListenAddr 设置监听地址（集成测试用）
func (m *Module) SetListenAddr(addr string) { m.listenAddr = addr }

// RemoteSeqMap 返回 RemoteSeqMap（集成测试用）
func (m *Module) RemoteSeqMap() *RemoteSeqMap { return m.remoteSeq }

// DeliverToLocalConn 投递给本地连接（集成测试用）
func (m *Module) DeliverToLocalConn(nodeID uint32, f Frame) {
	m.connMu.RLock()
	c := m.localConns[nodeID]
	m.connMu.RUnlock()
	if c != nil {
		_ = c.Send(f)
	}
}
