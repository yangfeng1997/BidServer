package routeragent

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"
)

// handleIncomingPeer 处理远端 RA 的入站 TCP 连接
func (m *Module) handleIncomingPeer(conn net.Conn, listenAddr string) {
	addr := conn.RemoteAddr().String()
	defer conn.Close()

	// 接收对端 Handshake（包含对端 listen address）
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return
	}
	addrLen := int(binary.BigEndian.Uint16(buf))
	if addrLen > 256 || addrLen <= 0 {
		return
	}
	peerAddr := make([]byte, addrLen)
	if _, err := io.ReadFull(conn, peerAddr); err != nil {
		return
	}
	peerListenAddr := string(peerAddr)

	// 发送本端 Handshake
	hsBuf := make([]byte, 2+len(listenAddr))
	binary.BigEndian.PutUint16(hsBuf[:2], uint16(len(listenAddr)))
	copy(hsBuf[2:], listenAddr)
	conn.Write(hsBuf)

	// 防双向重复建连：两边比较 listenAddr 字典序
	if !m.DedupPeer(listenAddr, peerListenAddr, "incoming") {
		m.metrics.PeerConnectFailTotal.Add(1)
		return
	}

	// 包装为 PeerLink
	pl := &tcpPeerLink{conn: conn, addr: peerListenAddr, sendCh: make(chan Frame, 64), done: make(chan struct{})}
	m.peerMgr.Attach(peerListenAddr, pl)
	m.metrics.PeerConnectTotal.Add(1)

	_ = addr

	// 读写循环
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
}

// handlePeerFrame 处理从远端 peer 收到的帧
func (m *Module) handlePeerFrame(f Frame) {
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
		m.deliverToLocal(head.FromNodeID, f)
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
	conn    net.Conn
	addr    string
	sendCh  chan Frame
	done    chan struct{}
	once    sync.Once
}

func (l *tcpPeerLink) Send(f Frame) error {
	select {
	case <-l.done:
		return io.EOF
	case l.sendCh <- f:
		return nil
	}
}

func (l *tcpPeerLink) Close() error {
	l.once.Do(func() {
		close(l.done)
	})
	return nil
}

func (l *tcpPeerLink) writeLoop(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-l.done:
			return
		case f := <-l.sendCh:
			data, _ := EncodeFrame(f)
			if _, err := l.conn.Write(data); err != nil {
				l.Close()
				return
			}
		}
	}
}

func (l *tcpPeerLink) readLoop(onFrame func(Frame)) {
	for {
		lenBuf := make([]byte, 4)
		if _, err := io.ReadFull(l.conn, lenBuf); err != nil {
			l.Close()
			return
		}
		length := int(binary.BigEndian.Uint32(lenBuf))
		if length < 3 || length > 16*1024*1024 {
			continue
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(l.conn, buf); err != nil {
			l.Close()
			return
		}
		data := make([]byte, 4+length)
		copy(data[:4], lenBuf)
		copy(data[4:], buf)
		f, err := DecodeFrame(data)
		if err != nil {
			continue
		}
		// 心跳
		if f.Type == FrameHeartbeat {
			l.Send(Frame{Type: FrameHeartbeat})
			continue
		}
		onFrame(f)
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
		sendCh: make(chan Frame, 64),
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

func (a *adaptNetConn) Read(b []byte) (int, error)           { return a.c.Read(b) }
func (a *adaptNetConn) Write(b []byte) (int, error)          { return a.c.Write(b) }
func (a *adaptNetConn) Close() error                          { return a.c.Close() }
func (a *adaptNetConn) LocalAddr() net.Addr                   { return &pipeAddr{name: "pipe"} }
func (a *adaptNetConn) RemoteAddr() net.Addr                  { return &pipeAddr{name: "pipe"} }
func (a *adaptNetConn) SetDeadline(t time.Time) error          { return nil }
func (a *adaptNetConn) SetReadDeadline(t time.Time) error     { return a.c.SetReadDeadline(t) }
func (a *adaptNetConn) SetWriteDeadline(t time.Time) error    { return a.c.SetWriteDeadline(t) }

type pipeAddr struct{ name string }
func (p *pipeAddr) Network() string { return "pipe" }
func (p *pipeAddr) String() string  { return p.name }
