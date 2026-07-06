package agent

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"project/internal/core/app"
	"project/internal/core/nodeid"
	"project/pkg/logger"
	"project/protocol/common"
)

const (
	defaultSockPath             = "/run/routeragent/ra.sock"
	incomingPeerSeqRetention    = 10 * time.Second
	incomingPeerSeqCleanupEvery = time.Second
	queueStatsLogEvery          = 5 * time.Second
)

// Runtime is the RouterAgent server-side runtime core.
type Runtime struct {
	poster      app.Poster
	ready       *app.Ready
	nodeID      uint32
	nodeIDText  string
	sockPath    string
	memberTable *MemberTable
	peerMgr     *PeerMgr
	resolver    *Resolver
	keepalive   *KeepAlive
	udsServer   *UDSServer
	tcpServer   *TCPServer
	registry    Registry
	listenAddr  string
	stopCh      chan struct{}

	localConns  *LocalConnSet
	remoteSeq   *RemoteSeqMap
	incomingSeq *IncomingPeerSeqStore
	peerFwd     *PeerForwarder
	router      *Router

	mu        sync.Mutex
	waiters   map[uint64]*BroadcastWaiter
	waiterSeq atomic.Uint64
	metrics   *Metrics
}

// NewRuntime 创建 routeragent runtime
func NewRuntime() *Runtime {
	m := &Runtime{
		ready:       app.NewReady(),
		sockPath:    defaultSockPath,
		memberTable: NewMemberTable(),
		peerMgr:     NewPeerMgr(),
		resolver:    NewResolver(),
		keepalive:   NewKeepAlive(5*time.Second, 10*time.Second),
		stopCh:      make(chan struct{}),
		localConns:  NewLocalConnSet(),
		incomingSeq: NewIncomingPeerSeqStore(),
		waiters:     make(map[uint64]*BroadcastWaiter),
		metrics:     NewMetrics(),
	}
	m.remoteSeq = NewRemoteSeqMap(&m.metrics.RemoteSeqPending)
	m.peerFwd = NewPeerForwarder(m.peerMgr, m.remoteSeq, m.localConns, m.memberTable, m.incomingSeq, m.metrics, m.listenAddr, m.poster)
	m.router = NewRouter(m.memberTable, m.resolver, m.localConns, m.remoteSeq, m.peerFwd, m.incomingSeq, m.metrics)
	m.peerFwd.SetOnPeerFrame(m.router.HandlePeerFrame)
	return m
}

func (m *Runtime) SetPoster(poster app.Poster) {
	m.poster = poster
	if m.peerFwd != nil {
		m.peerFwd.poster = poster
	}
}

func (m *Runtime) SetNodeID(text string, id uint32) {
	m.nodeIDText = text
	m.nodeID = id
}

func (m *Runtime) Init() error {
	logger.Info("routeragent init start", logger.String("node_id", m.nodeIDText), logger.Uint32("node_id_u32", m.nodeID))
	if m.poster == nil {
		m.poster = PosterFunc(func(fn func()) { fn() })
		logger.Warn("routeragent poster missing, use inline poster")
	}
	if m.listenAddr == "" {
		return fmt.Errorf("routeragent listen_addr is required")
	}
	logger.Info("routeragent listen addr ready", logger.String("listen_addr", m.listenAddr))
	m.peerFwd.poster = m.poster
	m.peerFwd.SetListenAddr(m.listenAddr)
	m.peerFwd.SetOnPeerFrame(m.router.HandlePeerFrame)
	logger.Info("routeragent init done", logger.String("listen_addr", m.listenAddr))
	return nil
}

// AfterInit 启动 routeragent 子组件
func (m *Runtime) AfterInit() error {
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
	go m.runQueueStatsLog(m.stopCh)

	if m.registry != nil {
		serverType := uint32(common.ServerType_ST_ROUTERAGENT)
		logger.Info("routeragent etcd self register start", logger.Uint32("node_id", m.nodeID), logger.String("listen_addr", m.listenAddr), logger.Uint32("server_type", serverType))
		if err := m.registry.Register(m.nodeID, m.listenAddr, serverType); err != nil {
			logger.Error("routeragent etcd self register failed", logger.Uint32("node_id", m.nodeID), logger.String("listen_addr", m.listenAddr), logger.Err(err))
			m.ready.Fail(err)
			return err
		}
		logger.Info("routeragent etcd self register done", logger.Uint32("node_id", m.nodeID), logger.String("listen_addr", m.listenAddr))
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
		if m.peerFwd != nil {
			m.peerFwd.SetListenAddr(m.listenAddr)
		}
		m.tcpServer = NewTCPServer(m.listenAddr, m.listenAddr, m.peerFwd.HandleIncomingPeer)
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
func (m *Runtime) WaitReady(ctx context.Context) error {
	return m.ready.WaitReady(ctx)
}

// BeforeShutdown 停止后台组件
func (m *Runtime) BeforeShutdown() {
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

func (m *Runtime) Shutdown() {}

func (m *Runtime) ApplyConfig(sockPath, listenAddr string, heartbeatSec int32) {
	if sockPath != "" {
		m.sockPath = sockPath
	}
	m.listenAddr = listenAddr
	if heartbeatSec > 0 {
		interval := time.Duration(heartbeatSec) * time.Second
		m.keepalive = NewKeepAlive(interval, interval*2)
	}
}

func (m *Runtime) SetRegistry(registry Registry) {
	m.registry = registry
}

func (m *Runtime) handleConn(c *UDSConn) {
	defer c.Close()
	for frame := range c.Recv() {
		frame := frame
		m.poster.Post(func() {
			m.handleFrame(c, frame)
		})
	}
	m.removeConn(c)
}

func (m *Runtime) handleFrame(c *UDSConn, frame Frame) {
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
		m.router.RouteFrame(c, frame)
	case FrameBroadcastSent:
		m.handleBroadcastSent(frame.Body)
	}
}

func (m *Runtime) trackIncomingPeerSeq(seqID uint64, peerKey string) {
	m.incomingSeq.Track(seqID, peerKey)
}

func (m *Runtime) cleanupIncomingPeerSeq(now time.Time) {
	m.incomingSeq.CleanupExpired(now, incomingPeerSeqRetention)
}

func (m *Runtime) cleanupIncomingPeerSeqByPeer(peerKey string) {
	m.incomingSeq.CleanupByPeer(peerKey)
}

func (m *Runtime) runIncomingPeerSeqCleanup(stop <-chan struct{}) {
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

func (m *Runtime) registerConn(nodeID uint32, c *UDSConn) {
	m.localConns.Register(nodeID, c)
}

func (m *Runtime) removeConn(c *UDSConn) {
	m.metrics.PeerDisconnectTotal.Add(1)
	removed := m.localConns.Remove(c)
	for _, nodeID := range removed {
		m.memberTable.Delete(nodeID)
		if m.registry != nil {
			_ = m.registry.DeleteNode(nodeID)
		}
	}
	m.remoteSeq.DeleteByConn(c)
}

func (m *Runtime) localConn(nodeID uint32) *UDSConn {
	return m.localConns.Get(nodeID)
}

func (m *Runtime) handleBroadcastSent(body []byte) {
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
func (m *Runtime) Broadcast(serverType uint32, payload []byte) BroadcastSentRecord {
	list := m.memberTable.ListByServerType(serverType)
	if len(list) == 0 {
		return BroadcastSentRecord{}
	}
	waiterID := m.waiterSeq.Add(1)
	nodeIDs := make([]uint32, 0, len(list))
	for _, info := range list {
		nodeIDs = append(nodeIDs, info.NodeID)
		_ = m.router.SendToNode(info.NodeID, Frame{Type: FrameRpcNotify, Body: EncodeRouteBody(info.NodeID, payload)})
	}
	m.RegisterWaiter(NewBroadcastWaiter(waiterID, nodeIDs))
	return BroadcastSentRecord{WaiterID: waiterID, NodeIDs: nodeIDs}
}
