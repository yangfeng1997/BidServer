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
	"project/pkg/logger"
	"project/protocol/common"
)

const (
	moduleName                  = "routeragent"
	defaultSockPath             = "/run/routeragent/ra.sock"
	incomingPeerSeqRetention    = 10 * time.Second
	incomingPeerSeqCleanupEvery = time.Second
)

type incomingPeerSeqEntry struct {
	peerKey string
	at      time.Time
}

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
	registry    *EtcdRegistry
	listenAddr  string
	stopCh      chan struct{}

	connMu     sync.RWMutex
	localConns map[uint32]*UDSConn

	mu        sync.Mutex
	waiters   map[uint64]*BroadcastWaiter
	waiterSeq atomic.Uint64
	remoteSeq *RemoteSeqMap
	metrics   *Metrics

	incomingPeerSeqMu sync.Mutex
	incomingPeerSeq   map[uint64]incomingPeerSeqEntry // seqID -> peer connection that delivered the request
}

// NewModule 创建 routeragent 模块
func NewModule() *Module {
	m := &Module{
		ready:           app.NewReady(),
		sockPath:        defaultSockPath,
		memberTable:     NewMemberTable(),
		peerMgr:         NewPeerMgr(),
		resolver:        NewResolver(),
		keepalive:       NewKeepAlive(5*time.Second, 10*time.Second),
		stopCh:          make(chan struct{}),
		localConns:      make(map[uint32]*UDSConn),
		waiters:         make(map[uint64]*BroadcastWaiter),
		metrics:         NewMetrics(),
		incomingPeerSeq: make(map[uint64]incomingPeerSeqEntry),
	}
	m.remoteSeq = NewRemoteSeqMap(&m.metrics.RemoteSeqPending)
	if cfg := RouteragentConfig(); cfg != nil {
		m.ApplyConfig(cfg.SockPath, cfg.ListenAddr, cfg.HeartbeatSec)
	}
	return m
}

func (m *Module) Name() string { return moduleName }

func (m *Module) Init() error {
	logger.Info("routeragent init start", logger.String("node_id", m.App().NodeID()), logger.Uint32("node_id_u32", m.App().NodeIDUint32()))
	if p, ok := m.App().(app.Poster); ok {
		m.poster = p
		logger.Info("routeragent poster use app poster")
	} else {
		m.poster = PosterFunc(func(fn func()) { fn() })
		logger.Warn("routeragent app poster missing, use inline poster")
	}
	if m.listenAddr == "" {
		return fmt.Errorf("routeragent listen_addr is required")
	}
	logger.Info("routeragent listen addr ready", logger.String("listen_addr", m.listenAddr))
	commonCfg := CommonConfig()
	if commonCfg != nil && m.App().NodeIDUint32() != 0 {
		prefix := NodePrefix(commonCfg.Cluster.Name, commonCfg.Cluster.Env, commonCfg.Cluster.WorldId)
		logger.Info("routeragent etcd registry init start",
			logger.String("cluster_name", commonCfg.Cluster.Name),
			logger.String("cluster_env", commonCfg.Cluster.Env),
			logger.Uint32("world_id", commonCfg.Cluster.WorldId),
			logger.String("prefix", prefix),
			logger.Any("endpoints", commonCfg.Etcd.Endpoints))
		registry, err := NewEtcdRegistry(commonCfg.Etcd.Endpoints, prefix)
		if err != nil {
			logger.Error("routeragent etcd registry init failed", logger.String("prefix", prefix), logger.Err(err))
			return err
		}
		m.registry = registry
		logger.Info("routeragent etcd registry init done", logger.String("prefix", prefix))
	} else {
		logger.Warn("routeragent etcd registry skipped", logger.Bool("common_config_nil", commonCfg == nil), logger.Uint32("node_id_u32", m.App().NodeIDUint32()))
	}
	logger.Info("routeragent init done", logger.String("listen_addr", m.listenAddr))
	return nil
}

// AfterInit 启动 routeragent 子组件
func (m *Module) AfterInit() error {
	logger.Info("routeragent after init start", logger.String("sock_path", m.sockPath), logger.String("listen_addr", m.listenAddr))
	m.udsServer = NewUDSServer(m.sockPath, m.handleConn)
	logger.Info("routeragent uds listen start", logger.String("sock_path", m.sockPath))
	if err := m.udsServer.Listen(); err != nil {
		logger.Error("routeragent uds listen failed", logger.String("sock_path", m.sockPath), logger.Err(err))
		m.ready.Fail(err)
		return err
	}
	logger.Info("routeragent uds listen done", logger.String("sock_path", m.sockPath))
	go m.udsServer.Serve(m.stopCh)
	logger.Info("routeragent keepalive start")
	go m.keepalive.Run(m.stopCh)
	go m.runIncomingPeerSeqCleanup(m.stopCh)

	if m.registry != nil {
		serverType := uint32(common.ServerType_ST_ROUTERAGENT)
		logger.Info("routeragent etcd self register start", logger.Uint32("node_id", m.App().NodeIDUint32()), logger.String("listen_addr", m.listenAddr), logger.Uint32("server_type", serverType))
		if err := m.registry.Register(m.App().NodeIDUint32(), m.listenAddr, serverType); err != nil {
			logger.Error("routeragent etcd self register failed", logger.Uint32("node_id", m.App().NodeIDUint32()), logger.String("listen_addr", m.listenAddr), logger.Err(err))
			m.ready.Fail(err)
			return err
		}
		logger.Info("routeragent etcd self register done", logger.Uint32("node_id", m.App().NodeIDUint32()), logger.String("listen_addr", m.listenAddr))
		logger.Info("routeragent etcd discover start")
		if err := m.registry.Discover(func(info NodeInfo, serverType uint32) {
			upsertInfo := info
			st := serverType
			m.poster.Post(func() {
				m.memberTable.Upsert(upsertInfo, st)
			})
		}, func(nodeID uint32) {
			m.poster.Post(func() {
				m.memberTable.Delete(nodeID)
			})
		}); err != nil {
			logger.Error("routeragent etcd discover failed", logger.Err(err))
			m.ready.Fail(err)
			return err
		}
		logger.Info("routeragent etcd discover done")
	}
	if m.listenAddr != "" {
		m.peerMgr.SetListenAddr(m.listenAddr)
		m.tcpServer = NewTCPServer(m.listenAddr, m.listenAddr, m.handleIncomingPeer)
		logger.Info("routeragent tcp listen start", logger.String("listen_addr", m.listenAddr))
		if err := m.tcpServer.Listen(); err != nil {
			logger.Error("routeragent tcp listen failed", logger.String("listen_addr", m.listenAddr), logger.Err(err))
			m.ready.Fail(err)
			return err
		}
		logger.Info("routeragent tcp listen done", logger.String("listen_addr", m.listenAddr))
		go m.tcpServer.Serve(m.stopCh)
	}
	logger.Info("routeragent after init done", logger.String("listen_addr", m.listenAddr), logger.String("sock_path", m.sockPath))
	m.ready.Done()
	return nil
}

// 等待首次就绪
func (m *Module) WaitReady(ctx context.Context) error {
	return m.ready.WaitReady(ctx)
}

// BeforeShutdown 停止后台组件
func (m *Module) BeforeShutdown() {
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
	if m.registry != nil {
		m.registry.Close()
	}
	if m.udsServer != nil {
		_ = m.udsServer.Close()
	}
	if m.tcpServer != nil {
		_ = m.tcpServer.Close()
	}
}

func (m *Module) Shutdown() {}

func (m *Module) ApplyConfig(sockPath, listenAddr string, heartbeatSec int32) {
	if sockPath != "" {
		m.sockPath = sockPath
	}
	m.listenAddr = listenAddr
	if heartbeatSec > 0 {
		interval := time.Duration(heartbeatSec) * time.Second
		m.keepalive = NewKeepAlive(interval, interval*2)
	}
}

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
			logger.Warn("routeragent business handshake invalid", logger.String("remote", c.RemoteAddr()), logger.Int("body_len", len(frame.Body)))
			return
		}
		nodeID := binary.BigEndian.Uint32(frame.Body[:4])
		_, serverType, _ := nodeid.Decode(nodeID)
		logger.Info("routeragent business handshake start", logger.String("remote", c.RemoteAddr()), logger.Uint32("node_id", nodeID), logger.String("node_id_text", nodeid.String(nodeID)), logger.Uint32("server_type", serverType), logger.String("listen_addr", m.listenAddr))
		if m.registry != nil {
			if err := m.registry.PutNode(nodeID, m.listenAddr, serverType); err != nil {
				logger.Error("routeragent business etcd register failed", logger.Uint32("node_id", nodeID), logger.String("node_id_text", nodeid.String(nodeID)), logger.Uint32("server_type", serverType), logger.String("listen_addr", m.listenAddr), logger.Err(err))
				_ = c.Send(Frame{Type: FrameHandshakeAck, Body: []byte{0}})
				return
			}
			logger.Info("routeragent business etcd register done", logger.Uint32("node_id", nodeID), logger.String("node_id_text", nodeid.String(nodeID)), logger.Uint32("server_type", serverType), logger.String("listen_addr", m.listenAddr))
		}
		m.memberTable.Upsert(NodeInfo{
			NodeID:  nodeID,
			RAAddr:  m.listenAddr,
			StartAt: time.Now().Unix(),
		}, serverType)
		m.registerConn(nodeID, c)
		logger.Info("routeragent business handshake done", logger.Uint32("node_id", nodeID), logger.String("node_id_text", nodeid.String(nodeID)), logger.Uint32("server_type", serverType))
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
		if head.DestNodeID != 0 {
			m.sendResponseViaPeer(head.SeqID, head.DestNodeID, frame)
			return
		}
		entry := m.remoteSeq.Pop(head.SeqID)
		if entry == nil || entry.udsConn == nil {
			return
		}
		head.SeqID = entry.origSeqID
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
	origSeqID := head.SeqID
	if len(targets) == 0 {
		return
	}
	for _, nodeID := range targets {
		info, ok := m.memberTable.GetByNodeID(nodeID)
		if !ok {
			continue
		}
		if local := m.localConn(nodeID); local != nil {
			out := frame
			head.DestNodeID = nodeID
			out.Header = EncodeRPCWireHeader(head)
			_ = local.Send(out)
			continue
		}
		_ = m.sendPeerOrQueue(info.RAAddr, head.ServerType, peerOutbound{
			source:       c,
			frame:        frame,
			head:         head,
			origSeqID:    origSeqID,
			targetNodeID: nodeID,
			prepareRPC:   true,
		})
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
	if local := m.localConn(nodeID); local != nil {
		_ = local.Send(frame)
		return
	}
	_ = m.sendPeerOrQueue(info.RAAddr, 0, peerOutbound{frame: Frame{Type: frame.Type, Body: EncodeRouteBody(nodeID, payload)}})
}

func (m *Module) sendToNode(nodeID uint32, frame Frame) error {
	if local := m.localConn(nodeID); local != nil {
		return local.Send(frame)
	}
	info, ok := m.memberTable.GetByNodeID(nodeID)
	if !ok {
		return errors.New("node not found")
	}
	return m.sendPeerOrQueue(info.RAAddr, 0, peerOutbound{frame: frame})
}

// sendResponseViaPeer 将应答通过原请求进入的 peer 连接送回，避免创建多余连接。
func (m *Module) sendResponseViaPeer(seqID uint64, destNodeID uint32, frame Frame) {
	if local := m.localConn(destNodeID); local != nil {
		_ = local.Send(frame)
		return
	}
	info, ok := m.memberTable.GetByNodeID(destNodeID)
	if !ok {
		return
	}
	m.incomingPeerSeqMu.Lock()
	entry, has := m.incomingPeerSeq[seqID]
	if has {
		delete(m.incomingPeerSeq, seqID)
	}
	m.incomingPeerSeqMu.Unlock()
	if has {
		if snap := m.peerMgr.getLink(entry.peerKey); snap.state == PeerConnected && snap.link != nil {
			_ = snap.link.Send(frame)
			return
		}
	}
	_ = m.sendPeerOrQueue(info.RAAddr, 0, peerOutbound{frame: frame})
}

func (m *Module) trackIncomingPeerSeq(seqID uint64, peerKey string) {
	if seqID == 0 || peerKey == "" {
		return
	}
	m.incomingPeerSeqMu.Lock()
	m.incomingPeerSeq[seqID] = incomingPeerSeqEntry{peerKey: peerKey, at: time.Now()}
	m.incomingPeerSeqMu.Unlock()
}

func (m *Module) cleanupIncomingPeerSeq(now time.Time) {
	m.incomingPeerSeqMu.Lock()
	for seqID, entry := range m.incomingPeerSeq {
		if now.Sub(entry.at) > incomingPeerSeqRetention {
			delete(m.incomingPeerSeq, seqID)
		}
	}
	m.incomingPeerSeqMu.Unlock()
}

func (m *Module) cleanupIncomingPeerSeqByPeer(peerKey string) {
	if peerKey == "" {
		return
	}
	m.incomingPeerSeqMu.Lock()
	for seqID, entry := range m.incomingPeerSeq {
		if entry.peerKey == peerKey {
			delete(m.incomingPeerSeq, seqID)
		}
	}
	m.incomingPeerSeqMu.Unlock()
}

func (m *Module) runIncomingPeerSeqCleanup(stop <-chan struct{}) {
	ticker := time.NewTicker(incomingPeerSeqCleanupEvery)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			m.cleanupIncomingPeerSeq(now)
		}
	}
}

func (m *Module) registerConn(nodeID uint32, c *UDSConn) {
	m.connMu.Lock()
	m.localConns[nodeID] = c
	m.connMu.Unlock()
}

func (m *Module) removeConn(c *UDSConn) {
	m.metrics.PeerDisconnectTotal.Add(1)
	removed := make([]uint32, 0, 1)
	m.connMu.Lock()
	for nodeID, conn := range m.localConns {
		if conn == c {
			delete(m.localConns, nodeID)
			removed = append(removed, nodeID)
		}
	}
	m.connMu.Unlock()
	for _, nodeID := range removed {
		m.memberTable.Delete(nodeID)
		if m.registry != nil {
			_ = m.registry.DeleteNode(nodeID)
		}
	}
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
