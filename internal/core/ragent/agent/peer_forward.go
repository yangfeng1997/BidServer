package agent

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"project/internal/core/app"
	"project/internal/core/errcode"
	"project/pkg/logger"
)

// PeerForwarder 负责跨 RouterAgent 的 peer 连接建立、帧发送和入站 peer 处理。
// 它是 RouterAgent 的 peer 转发层组件，桥接 routing 和 transport。
type PeerForwarder struct {
	peerMgr     *PeerMgr
	remoteSeq   *RemoteSeqMap
	localConns  *LocalConnSet
	memberTable *MemberTable
	incomingSeq *IncomingPeerSeqStore
	metrics     *Metrics
	listenAddr  string
	poster      app.Poster

	// onPeerFrame 在 peer 收到帧时被调用，委托给 Router.HandlePeerFrame。
	onPeerFrame func(Frame, string)
}

// NewPeerForwarder 创建 peer 转发器。
func NewPeerForwarder(peerMgr *PeerMgr, remoteSeq *RemoteSeqMap, localConns *LocalConnSet, memberTable *MemberTable, incomingSeq *IncomingPeerSeqStore, metrics *Metrics, listenAddr string, poster app.Poster) *PeerForwarder {
	return &PeerForwarder{
		peerMgr:     peerMgr,
		remoteSeq:   remoteSeq,
		localConns:  localConns,
		memberTable: memberTable,
		incomingSeq: incomingSeq,
		metrics:     metrics,
		listenAddr:  listenAddr,
		poster:      poster,
	}
}

// SetListenAddr 更新监听地址（配置加载后调用）。
func (f *PeerForwarder) SetListenAddr(addr string) { f.listenAddr = addr }

// SetOnPeerFrame 设置 peer 帧回调，由 Runtime 在装配时调用。
func (f *PeerForwarder) SetOnPeerFrame(fn func(Frame, string)) { f.onPeerFrame = fn }

func (f *PeerForwarder) post(fn func()) {
	if f.poster != nil {
		f.poster.Post(fn)
		return
	}
	fn()
}

// SendOrQueue 发送帧到指定 peer 地址，未连接时入队并触发异步拨号。
func (f *PeerForwarder) SendOrQueue(addr string, serverType uint32, item peerOutbound) error {
	if addr == "" {
		f.FailOutbound(item, errcode.ERR_NO_ROUTE)
		return errors.New("peer addr is empty")
	}
	key := peerKey(addr, serverType)
	if snap := f.peerMgr.getLink(key); snap.state == PeerConnected && snap.link != nil {
		if err := f.sendPeerOutbound(snap.link, item); err == nil {
			return nil
		} else {
			logger.Warn("routeragent peer send failed, queue and reconnect", logger.String("peer_key", key), logger.Err(err))
		}
	}
	startDial, err := f.peerMgr.enqueue(key, item)
	if err != nil {
		logger.Error("routeragent peer enqueue failed", logger.String("peer_key", key), logger.Err(err))
		f.FailOutbound(item, errcode.ERR_INTERNAL)
		return err
	}
	if startDial {
		f.startDial(addr, serverType)
	}
	return nil
}

func (f *PeerForwarder) sendPeerOutbound(link PeerLink, item peerOutbound) error {
	frame := item.frame
	if item.prepareRPC {
		head := item.head
		if frame.Type == FrameRpcRequest {
			remoteSeq := f.remoteSeq.Alloc(item.source, item.origSeqID)
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
				f.remoteSeq.Pop(head.SeqID)
			}
			f.FailOutbound(item, errcode.ERR_INTERNAL)
			return err
		}
		return nil
	}
	if err := link.Send(frame); err != nil {
		f.FailOutbound(item, errcode.ERR_INTERNAL)
		return err
	}
	return nil
}

// FailOutbound 在发送失败时向源连接回错误响应。
func (f *PeerForwarder) FailOutbound(item peerOutbound, code errcode.ErrCode) {
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

// SendResponseViaPeer 将应答通过原请求进入的 peer 连接送回。
func (f *PeerForwarder) SendResponseViaPeer(seqID uint64, destNodeID uint32, frame Frame) {
	if local := f.localConns.Get(destNodeID); local != nil {
		_ = local.Send(frame)
		return
	}
	info, ok := f.memberTable.GetByNodeID(destNodeID)
	if !ok {
		return
	}
	peerKey, has := f.incomingSeq.Pop(seqID)
	if has {
		if snap := f.peerMgr.getLink(peerKey); snap.state == PeerConnected && snap.link != nil {
			_ = snap.link.Send(frame)
			return
		}
	}
	_ = f.SendOrQueue(info.RAAddr, 0, peerOutbound{frame: frame})
}

func (f *PeerForwarder) startDial(addr string, serverType uint32) {
	key := peerKey(addr, serverType)
	logger.Info("routeragent peer async dial scheduled", logger.String("peer_addr", addr), logger.Uint32("server_type", serverType))
	go func() {
		pl, peerListenAddr, err := f.dialConn(addr, serverType)
		f.post(func() {
			if err != nil {
				pending := f.peerMgr.failPending(key)
				for _, item := range pending {
					f.FailOutbound(item, errcode.ERR_INTERNAL)
				}
				return
			}
			peerKey := peerKey(peerListenAddr, serverType)
			old, replaced, pending := f.peerMgr.Attach(peerKey, pl, "outgoing")
			if replaced {
				logger.Warn("routeragent peer replaced old connection", logger.String("direction", "outgoing"), logger.String("peer_key", peerKey), logger.String("listen_addr", f.peerMgr.listenAddr))
				f.metrics.PeerDisconnectTotal.Add(1)
				_ = old.Close()
			}
			f.metrics.PeerConnectTotal.Add(1)
			logger.Info("routeragent peer connected", logger.String("direction", "outgoing"), logger.String("peer_key", peerKey), logger.Int("pending", len(pending)))
			f.startLoops(pl, peerKey, "outgoing", peerKey)
			f.FlushPending(pl, pending)
		})
	}()
}

// Dial 同步连接远端 RA。保留给集成测试和手工调用。
func (f *PeerForwarder) Dial(addr string) error {
	serverType := uint32(0)
	pl, peerListenAddr, err := f.dialConn(addr, serverType)
	if err != nil {
		return err
	}
	peerKey := peerKey(peerListenAddr, serverType)
	old, replaced, pending := f.peerMgr.Attach(peerKey, pl, "outgoing")
	if replaced {
		logger.Warn("routeragent peer replaced old connection", logger.String("direction", "outgoing"), logger.String("peer_key", peerKey), logger.String("listen_addr", f.peerMgr.listenAddr))
		f.metrics.PeerDisconnectTotal.Add(1)
		_ = old.Close()
	}
	f.metrics.PeerConnectTotal.Add(1)
	logger.Info("routeragent peer connected", logger.String("direction", "outgoing"), logger.String("peer_key", peerKey), logger.String("listen_addr", f.peerMgr.listenAddr))
	f.startLoops(pl, peerKey, "outgoing", peerKey)
	f.FlushPending(pl, pending)
	return nil
}

func (f *PeerForwarder) dialConn(addr string, serverType uint32) (*tcpPeerLink, string, error) {
	if addr == "" {
		logger.Warn("routeragent peer dial skipped: empty addr")
		return nil, "", errors.New("peer addr is empty")
	}
	listenAddr := f.peerMgr.listenAddr
	key := peerKey(addr, serverType)
	logger.Info("routeragent peer dial start", logger.String("peer_addr", addr), logger.Uint32("server_type", serverType), logger.String("listen_addr", listenAddr))
	f.peerMgr.SetState(key, PeerConnecting)
	dialer := net.Dialer{Timeout: peerDialTimeout}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		logger.Error("routeragent peer dial failed", logger.String("peer_addr", addr), logger.Uint32("server_type", serverType), logger.String("listen_addr", listenAddr), logger.Err(err))
		f.peerMgr.SetState(key, PeerDisconnected)
		f.metrics.PeerConnectFailTotal.Add(1)
		return nil, "", err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	_ = conn.SetDeadline(time.Now().Add(peerDialTimeout))
	logger.Info("routeragent peer tcp connected", logger.String("peer_addr", addr), logger.String("local_addr", conn.LocalAddr().String()), logger.String("remote_addr", conn.RemoteAddr().String()))

	hsBuf := make([]byte, 2+len(listenAddr)+4)
	binary.BigEndian.PutUint16(hsBuf[:2], uint16(len(listenAddr)))
	copy(hsBuf[2:2+len(listenAddr)], listenAddr)
	binary.BigEndian.PutUint32(hsBuf[2+len(listenAddr):], serverType)
	logger.Info("routeragent peer handshake send", logger.String("peer_addr", addr), logger.String("listen_addr", listenAddr), logger.Uint32("server_type", serverType))
	if _, err := conn.Write(hsBuf); err != nil {
		logger.Error("routeragent peer handshake send failed", logger.String("peer_addr", addr), logger.String("listen_addr", listenAddr), logger.Err(err))
		_ = conn.Close()
		f.peerMgr.SetState(key, PeerDisconnected)
		f.metrics.PeerConnectFailTotal.Add(1)
		return nil, "", err
	}

	buf := make([]byte, 2)
	logger.Info("routeragent peer handshake receive start", logger.String("peer_addr", addr))
	if _, err := io.ReadFull(conn, buf); err != nil {
		logger.Error("routeragent peer handshake receive header failed", logger.String("peer_addr", addr), logger.Err(err))
		_ = conn.Close()
		f.peerMgr.SetState(key, PeerDisconnected)
		f.metrics.PeerConnectFailTotal.Add(1)
		return nil, "", err
	}
	peerAddrLen := int(binary.BigEndian.Uint16(buf))
	if peerAddrLen > 256 || peerAddrLen <= 0 {
		logger.Warn("routeragent peer handshake invalid addr length", logger.String("peer_addr", addr), logger.Int("addr_len", peerAddrLen))
		_ = conn.Close()
		f.peerMgr.SetState(key, PeerDisconnected)
		return nil, "", errors.New("invalid peer addr length")
	}
	peerAddrBuf := make([]byte, peerAddrLen)
	if _, err := io.ReadFull(conn, peerAddrBuf); err != nil {
		logger.Error("routeragent peer handshake receive addr failed", logger.String("peer_addr", addr), logger.Int("addr_len", peerAddrLen), logger.Err(err))
		_ = conn.Close()
		f.peerMgr.SetState(key, PeerDisconnected)
		f.metrics.PeerConnectFailTotal.Add(1)
		return nil, "", err
	}
	_ = conn.SetDeadline(time.Time{})
	peerListenAddr := string(peerAddrBuf)
	stResp := make([]byte, 4)
	io.ReadFull(conn, stResp)
	logger.Info("routeragent peer handshake receive done", logger.String("peer_addr", addr), logger.String("peer_listen_addr", peerListenAddr), logger.String("listen_addr", listenAddr))
	pl := &tcpPeerLink{conn: conn, addr: peerListenAddr, sendCh: make(chan Frame, 16384), prioSendCh: make(chan Frame, 4096), done: make(chan struct{})}
	return pl, peerListenAddr, nil
}

// FlushPending 把积压的出站帧刷到新连接上。
func (f *PeerForwarder) FlushPending(link PeerLink, pending []peerOutbound) {
	for _, item := range pending {
		if err := f.sendPeerOutbound(link, item); err != nil {
			logger.Error("routeragent peer flush pending failed", logger.Err(err))
		}
	}
}

func (f *PeerForwarder) startLoops(pl *tcpPeerLink, peerListenAddr string, direction string, remoteAddr string) {
	go func() {
		writeDone := make(chan struct{})
		go pl.writeLoop(writeDone)
		pl.readLoop(func(frame Frame) {
			f.post(func() {
				if f.onPeerFrame != nil {
					f.onPeerFrame(frame, peerListenAddr)
				}
			})
		})
		close(writeDone)
		if f.peerMgr.Detach(peerListenAddr, pl) {
			f.incomingSeq.CleanupByPeer(peerListenAddr)
			f.metrics.PeerDisconnectTotal.Add(1)
			logger.Warn("routeragent peer disconnected", logger.String("direction", direction), logger.String("peer_key", peerListenAddr), logger.String("remote_addr", remoteAddr))
		}
	}()
}

// HandleIncomingPeer 处理远端 RA 的入站 TCP 连接。
func (f *PeerForwarder) HandleIncomingPeer(conn net.Conn, listenAddr string) {
	addr := conn.RemoteAddr().String()
	logger.Info("routeragent peer incoming accepted", logger.String("remote_addr", addr), logger.String("listen_addr", listenAddr))
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	buf := make([]byte, 2)
	logger.Info("routeragent peer incoming handshake receive start", logger.String("remote_addr", addr))
	if _, err := io.ReadFull(conn, buf); err != nil {
		logger.Error("routeragent peer incoming handshake receive header failed", logger.String("remote_addr", addr), logger.Err(err))
		return
	}
	addrLen := int(binary.BigEndian.Uint16(buf))
	if addrLen > 256 || addrLen <= 0 {
		logger.Warn("routeragent peer incoming handshake invalid addr length", logger.String("remote_addr", addr), logger.Int("addr_len", addrLen))
		return
	}
	peerAddr := make([]byte, addrLen)
	if _, err := io.ReadFull(conn, peerAddr); err != nil {
		logger.Error("routeragent peer incoming handshake receive addr failed", logger.String("remote_addr", addr), logger.Int("addr_len", addrLen), logger.Err(err))
		return
	}
	peerListenAddr := string(peerAddr)
	logger.Info("routeragent peer incoming handshake receive done", logger.String("remote_addr", addr), logger.String("peer_listen_addr", peerListenAddr), logger.String("listen_addr", listenAddr))

	serverType := uint32(0)
	stBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, stBuf); err != nil {
		logger.Warn("routeragent peer incoming handshake serverType read failed, using 0", logger.String("remote_addr", addr), logger.Err(err))
	} else {
		serverType = binary.BigEndian.Uint32(stBuf)
	}
	peerKey := peerKey(peerListenAddr, serverType)
	logger.Info("routeragent peer incoming handshake serverType", logger.String("remote_addr", addr), logger.Uint32("server_type", serverType), logger.String("peer_key", peerKey))

	hsBuf := make([]byte, 2+len(listenAddr)+4)
	binary.BigEndian.PutUint16(hsBuf[:2], uint16(len(listenAddr)))
	copy(hsBuf[2:2+len(listenAddr)], listenAddr)
	logger.Info("routeragent peer incoming handshake send", logger.String("remote_addr", addr), logger.String("listen_addr", listenAddr), logger.String("peer_key", peerKey))
	if _, err := conn.Write(hsBuf); err != nil {
		logger.Error("routeragent peer incoming handshake send failed", logger.String("remote_addr", addr), logger.String("listen_addr", listenAddr), logger.Err(err))
		f.metrics.PeerConnectFailTotal.Add(1)
		return
	}

	pl := &tcpPeerLink{conn: conn, addr: peerListenAddr, sendCh: make(chan Frame, 16384), prioSendCh: make(chan Frame, 4096), done: make(chan struct{})}
	old, replaced, pending := f.peerMgr.Attach(peerKey, pl, "incoming")
	if replaced {
		logger.Warn("routeragent peer replaced old connection", logger.String("direction", "incoming"), logger.String("peer_key", peerKey), logger.String("listen_addr", listenAddr))
		f.metrics.PeerDisconnectTotal.Add(1)
		_ = old.Close()
	}
	f.metrics.PeerConnectTotal.Add(1)
	logger.Info("routeragent peer connected", logger.String("direction", "incoming"), logger.String("remote_addr", addr), logger.String("peer_key", peerKey), logger.String("listen_addr", listenAddr), logger.Int("pending", len(pending)))
	writeDone := make(chan struct{})
	go pl.writeLoop(writeDone)
	f.FlushPending(pl, pending)

	pl.readLoop(func(frame Frame) {
		f.post(func() {
			if f.onPeerFrame != nil {
				f.onPeerFrame(frame, peerKey)
			}
		})
	})
	close(writeDone)

	if f.peerMgr.Detach(peerKey, pl) {
		f.incomingSeq.CleanupByPeer(peerKey)
		f.metrics.PeerDisconnectTotal.Add(1)
		logger.Warn("routeragent peer disconnected", logger.String("direction", "incoming"), logger.String("peer_key", peerKey), logger.String("remote_addr", addr))
	}
}

// ForwardFrame 转发帧到指定 peer（测试用）。
func (f *PeerForwarder) ForwardFrame(addr string, frame Frame) error {
	return f.SendOrQueue(addr, 0, peerOutbound{frame: frame})
}

// Handshake 发送握手帧（测试用）。
func (f *PeerForwarder) Handshake(addr string, nodeID uint32) error {
	body := make([]byte, 4)
	binary.BigEndian.PutUint32(body, nodeID)
	return f.SendOrQueue(addr, 0, peerOutbound{frame: Frame{Type: FrameHandshake, Body: body}})
}
