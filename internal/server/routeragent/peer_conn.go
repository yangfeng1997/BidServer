package routeragent

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"project/pkg/logger"
)

// handleIncomingPeer 处理远端 RA 的入站 TCP 连接
func (m *Module) handleIncomingPeer(conn net.Conn, listenAddr string) {
	addr := conn.RemoteAddr().String()
	logger.Info("routeragent peer incoming accepted", logger.String("remote_addr", addr), logger.String("listen_addr", listenAddr))
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	// 接收对端 Handshake（包含对端 listen address）
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

	// 读取对端 serverType（握手格式：2B len + addr + 4B serverType）
	serverType := uint32(0)
	stBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, stBuf); err != nil {
		logger.Warn("routeragent peer incoming handshake serverType read failed, using 0", logger.String("remote_addr", addr), logger.Err(err))
	} else {
		serverType = binary.BigEndian.Uint32(stBuf)
	}
	peerKey := peerKey(peerListenAddr, serverType)
	logger.Info("routeragent peer incoming handshake serverType", logger.String("remote_addr", addr), logger.Uint32("server_type", serverType), logger.String("peer_key", peerKey))

	// 发送本端 Handshake
	hsBuf := make([]byte, 2+len(listenAddr)+4)
	binary.BigEndian.PutUint16(hsBuf[:2], uint16(len(listenAddr)))
	copy(hsBuf[2:2+len(listenAddr)], listenAddr)
	// serverType=0 已零值
	logger.Info("routeragent peer incoming handshake send", logger.String("remote_addr", addr), logger.String("listen_addr", listenAddr), logger.String("peer_key", peerKey))
	if _, err := conn.Write(hsBuf); err != nil {
		logger.Error("routeragent peer incoming handshake send failed", logger.String("remote_addr", addr), logger.String("listen_addr", listenAddr), logger.Err(err))
		m.metrics.PeerConnectFailTotal.Add(1)
		return
	}

	// 包装为 PeerLink。若双边同时建连，新连接会替换旧连接并主动关闭旧连接，避免残留双连接。
	pl := &tcpPeerLink{conn: conn, addr: peerListenAddr, sendCh: make(chan Frame, 16384), done: make(chan struct{})}
	old, replaced, pending := m.peerMgr.Attach(peerKey, pl, "incoming")
	if replaced {
		logger.Warn("routeragent peer replaced old connection", logger.String("direction", "incoming"), logger.String("peer_key", peerKey), logger.String("listen_addr", listenAddr))
		m.metrics.PeerDisconnectTotal.Add(1)
		_ = old.Close()
	}
	m.metrics.PeerConnectTotal.Add(1)
	logger.Info("routeragent peer connected", logger.String("direction", "incoming"), logger.String("remote_addr", addr), logger.String("peer_key", peerKey), logger.String("listen_addr", listenAddr), logger.Int("pending", len(pending)))
	writeDone := make(chan struct{})
	go pl.writeLoop(writeDone)
	m.flushPeerPending(pl, pending)

	pl.readLoop(func(f Frame) {
		m.post(func() {
			m.handlePeerFrame(f, peerKey)
		})
	})
	close(writeDone)

	if m.peerMgr.Detach(peerKey, pl) {
		m.metrics.PeerDisconnectTotal.Add(1)
		logger.Warn("routeragent peer disconnected", logger.String("direction", "incoming"), logger.String("peer_key", peerKey), logger.String("remote_addr", addr))
	}
}

// handlePeerFrame 处理从远端 peer 收到的帧
func (m *Module) handlePeerFrame(f Frame, peerKey string) {
	switch f.Type {
	case FrameRpcResponse:
		head, err := DecodeRPCWireHeader(f.Header)
		if err != nil {
			return
		}
		entry := m.remoteSeq.Pop(head.SeqID)
		if entry != nil && entry.udsConn != nil {
			head.SeqID = entry.origSeqID
			_ = entry.udsConn.Send(Frame{Type: FrameRpcResponse, Header: EncodeRPCWireHeader(head), Body: f.Body})
		} else {
			m.metrics.LateResponse.Add(1)
		}
		m.metrics.RemoteSeqPending.Add(-1)
		m.metrics.ForwardTotal.Add(1)
	case FrameRpcRequest, FrameRpcNotify:
		m.metrics.ForwardTotal.Add(1)
		head, err := DecodeRPCWireHeader(f.Header)
		if err != nil {
			return
		}
		nodeID := head.DestNodeID
		if nodeID == 0 {
			parsed, err := parseNodeIDKey(head.RoutingKey)
			if err != nil {
				m.metrics.RouteMiss.Add(1)
				return
			}
			nodeID = parsed
		}
		m.incomingPeerSeqMu.Lock()
		m.incomingPeerSeq[head.SeqID] = peerKey
		m.incomingPeerSeqMu.Unlock()
		m.deliverToLocal(nodeID, f)
	}
}

func (m *Module) deliverToLocal(nodeID uint32, f Frame) {
	m.connMu.RLock()
	c := m.localConns[nodeID]
	m.connMu.RUnlock()
	if c == nil {
		m.metrics.RouteMiss.Add(1)
		return
	}
	_ = c.Send(f)
}

// tcpPeerLink 将 net.Conn 适配为 PeerLink
type tcpPeerLink struct {
	conn   net.Conn
	addr   string
	sendCh chan Frame
	done   chan struct{}
	once   sync.Once
}

func (l *tcpPeerLink) Send(f Frame) error {
	select {
	case <-l.done:
		return io.EOF
	case l.sendCh <- f:
		return nil
	default:
		return errors.New("peer send queue full")
	}
}

func (l *tcpPeerLink) Close() error {
	var err error
	l.once.Do(func() {
		close(l.done)
		if l.conn != nil {
			err = l.conn.Close()
		}
	})
	return err
}

const tcpWriteBatchMaxFrames = 16

func (l *tcpPeerLink) writeLoop(done <-chan struct{}) {
	var buf bytes.Buffer
	for {
		select {
		case <-done:
			return
		case <-l.done:
			return
		case f := <-l.sendCh:
			data, _ := EncodeFrame(f)
			buf.Write(data)
			for drained := 1; drained < tcpWriteBatchMaxFrames; drained++ {
				select {
				case f2 := <-l.sendCh:
					data2, _ := EncodeFrame(f2)
					buf.Write(data2)
				default:
					goto flushNow
				}
			}
		flushNow:
			if _, err := l.conn.Write(buf.Bytes()); err != nil {
				l.Close()
				return
			}
			buf.Reset()
		}
	}
}

func (l *tcpPeerLink) readLoop(onFrame func(Frame)) {
	buf := make([]byte, 0, 65536)
	tmp := make([]byte, 32768)
	for {
		n, err := l.conn.Read(tmp)
		if err != nil {
			l.Close()
			return
		}
		buf = append(buf, tmp[:n]...)
		for len(buf) >= 4 {
			length := int(binary.BigEndian.Uint32(buf[:4]))
			if length < 3 || length > 16*1024*1024 {
				l.Close()
				return
			}
			total := 4 + length
			if len(buf) < total {
				break
			}
			f, err := DecodeFrame(buf[:total])
			if err == nil {
				if f.Type == FrameHeartbeat {
					l.Send(Frame{Type: FrameHeartbeat})
				} else {
					onFrame(f)
				}
			}
			buf = buf[total:]
		}
	}
}

var _ PeerLink = (*tcpPeerLink)(nil)

// NewTCPPeerLink 创建用于集成测试的 TCP 对端连接（自动启动 writeLoop）
func NewTCPPeerLink(conn interface{}, addr string) PeerLink {
	c, ok := conn.(interface {
		Read([]byte) (int, error)
		Write([]byte) (int, error)
		Close() error
		SetReadDeadline(time.Time) error
		SetWriteDeadline(time.Time) error
	})
	if !ok {
		return nil
	}
	pl := &tcpPeerLink{
		conn:   &adaptNetConn{c: c},
		addr:   addr,
		sendCh: make(chan Frame, 16384),
		done:   make(chan struct{}),
	}
	go pl.writeLoop(make(chan struct{}))
	return pl
}

type adaptNetConn struct {
	c interface {
		Read([]byte) (int, error)
		Write([]byte) (int, error)
		Close() error
		SetReadDeadline(time.Time) error
		SetWriteDeadline(time.Time) error
	}
}

func (a *adaptNetConn) Read(b []byte) (int, error)         { return a.c.Read(b) }
func (a *adaptNetConn) Write(b []byte) (int, error)        { return a.c.Write(b) }
func (a *adaptNetConn) Close() error                       { return a.c.Close() }
func (a *adaptNetConn) LocalAddr() net.Addr                { return &pipeAddr{name: "pipe"} }
func (a *adaptNetConn) RemoteAddr() net.Addr               { return &pipeAddr{name: "pipe"} }
func (a *adaptNetConn) SetDeadline(t time.Time) error      { return nil }
func (a *adaptNetConn) SetReadDeadline(t time.Time) error  { return a.c.SetReadDeadline(t) }
func (a *adaptNetConn) SetWriteDeadline(t time.Time) error { return a.c.SetWriteDeadline(t) }

type pipeAddr struct{ name string }

func (p *pipeAddr) Network() string { return "pipe" }
func (p *pipeAddr) String() string  { return p.name }
