package routeragent

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// tcpPeerLink 将 net.Conn 适配为 PeerLink
type tcpPeerLink struct {
	conn       net.Conn
	addr       string
	sendCh     chan Frame
	prioSendCh chan Frame
	done       chan struct{}
	once       sync.Once
	maxSendLen atomic.Int64
	maxPrioLen atomic.Int64
}

func (l *tcpPeerLink) Send(f Frame) error {
	ch := l.sendCh
	if isPriorityFrame(f.Type) {
		ch = l.prioSendCh
	}
	select {
	case <-l.done:
		return io.EOF
	case ch <- f:
		l.recordQueueLen()
		return nil
	default:
		return errors.New("peer send queue full")
	}
}

func isPriorityFrame(t FrameType) bool {
	return t == FrameRpcResponse || t == FrameHeartbeat || t == FrameHandshake || t == FrameHandshakeAck
}

func (l *tcpPeerLink) recordQueueLen() {
	if l.sendCh != nil {
		updateMaxInt64(&l.maxSendLen, int64(len(l.sendCh)))
	}
	if l.prioSendCh != nil {
		updateMaxInt64(&l.maxPrioLen, int64(len(l.prioSendCh)))
	}
}

func updateMaxInt64(dst *atomic.Int64, v int64) {
	for {
		old := dst.Load()
		if v <= old || dst.CompareAndSwap(old, v) {
			return
		}
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
	buf := make([]byte, 0, 65536)
	for {
		select {
		case <-done:
			return
		case <-l.done:
			return
		case f := <-l.prioSendCh:
			buf = l.writeBatch(buf, f)
		case f := <-l.sendCh:
			select {
			case pf := <-l.prioSendCh:
				buf = l.writeBatch(buf, pf)
				if err := l.Send(f); err != nil {
					l.Close()
					return
				}
			default:
				buf = l.writeBatch(buf, f)
			}
		}
	}
}

func (l *tcpPeerLink) writeBatch(buf []byte, first Frame) []byte {
	var err error
	buf, err = AppendFrame(buf[:0], first)
	if err != nil {
		return buf[:0]
	}
	for drained := 1; drained < tcpWriteBatchMaxFrames; drained++ {
		select {
		case f := <-l.prioSendCh:
			buf, _ = AppendFrame(buf, f)
		default:
			select {
			case f := <-l.sendCh:
				buf, _ = AppendFrame(buf, f)
			default:
				goto flushNow
			}
		}
	}
flushNow:
	if _, err := l.conn.Write(buf); err != nil {
		l.Close()
	}
	return buf[:0]
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
		conn:       &adaptNetConn{c: c},
		addr:       addr,
		sendCh:     make(chan Frame, 16384),
		prioSendCh: make(chan Frame, 4096),
		done:       make(chan struct{}),
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
