package gatesvr

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"project/internal/core/codec"
	"project/internal/core/dispatcher"
	corerpc "project/internal/core/rpc"
	"project/internal/core/session"
	genhandler "project/protocol/gen/handler"
	genroutes "project/protocol/gen"
	handlerpb "project/protocol/handler"
)

// TestE2E_TCPClientToHandler 端到端测试：真实 TCP 客户端→gate→dispatcher→Handler
func TestE2E_TCPClientToHandler(t *testing.T) {
	genroutes.RouteTable[2050] = genroutes.RouteEntry{
		ServerType: 1,
		Route:      "LobbyHandler/ClaimReward",
		RspCmdID:   2051,
	}
	genroutes.AuthWhitelist[2050] = true

	var handlerCalled sync.WaitGroup
	handlerCalled.Add(1)
	mainLoop := &syncPoster3{}

	m := NewModule("127.0.0.1:0")
	m.poster = mainLoop
	m.sessions = session.NewSessionManager()
	m.pending = NewPendingMap()
	m.dispatcher = dispatcher.NewGateDispatcher(1, m.sessions)
	m.dispatcher.Use(dispatcher.RecoverMiddleware())
	m.dispatcher.Use(dispatcher.AuthMiddleware(genroutes.AuthWhitelist))
	m.dispatcher.SetHandshakeHandler(m.handleHandshake)
	m.dispatcher.RegisterRoute(2050, dispatcher.RouteEntry{
		CmdID: 2050, ServerType: 1, Route: "LobbyHandler/ClaimReward", RspCmdID: 2051,
	})
	genhandler.RegisterLobbyHandler(m.dispatcher.Dispatcher, &e2eLobbyHandler{
		onClaim: func(req *handlerpb.CS_ClaimReward_Req, reply corerpc.Reply[*handlerpb.SC_ClaimReward_Rsp]) {
			handlerCalled.Done()
			reply(&handlerpb.SC_ClaimReward_Rsp{}, nil)
		},
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().String()
	t.Logf("gate: %s", addr)

	stopAccept := make(chan struct{})
	defer close(stopAccept)

	// main loop + accept combined in one goroutine
	go func() {
		for {
			select {
			case <-stopAccept:
				return
			default:
			}
			ln.(*net.TCPListener).SetDeadline(time.Now().Add(20 * time.Millisecond))
			raw, err := ln.Accept()
			if err == nil {
				go m.handleRawConn(raw)
			}
			mainLoop.drain()
		}
	}()

	time.Sleep(100 * time.Millisecond)

	// ---- TCP 客户端 ----
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	t.Logf("client connected")

	// 1) 握手
	hsBody := make([]byte, 3)
	binary.BigEndian.PutUint16(hsBody[0:2], 1)
	hsBody[2] = 1
	hsPkt, _ := codec.EncodePacket(codec.Packet{Type: codec.PacketHandshake, Body: hsBody})
	conn.Write(hsPkt)

	time.Sleep(150 * time.Millisecond)

	// 读 HandshakeAck
	conn.SetReadDeadline(time.Now().Add(time.Second))
	ackBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, ackBuf); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	ackBL := int(ackBuf[1])<<16 | int(ackBuf[2])<<8 | int(ackBuf[3])
	t.Logf("← HandshakeAck type=%d len=%d", ackBuf[0], ackBL)
	if ackBuf[0] != 0x02 {
		t.Errorf("expected HandshakeAck(0x02), got 0x%02x", ackBuf[0])
	}
	if ackBL > 0 {
		ackBody := make([]byte, ackBL)
		io.ReadFull(conn, ackBody)
		errCode := binary.BigEndian.Uint32(ackBody[0:4])
		t.Logf("  err_code=%d heartbeat=%d", errCode, binary.BigEndian.Uint16(ackBody[4:6]))
		if errCode != 0 {
			t.Fatalf("handshake rejected: err_code=%d", errCode)
		}
	}

	// 2) 发送数据
	msg, _ := codec.EncodeMessage(codec.Message{
		Type:  codec.MessageRequest,
		SeqID: 1,
		CmdID: 2050,
		Body:  []byte{0x08, 0x01},
	})
	dataPkt, _ := codec.EncodePacket(codec.Packet{Type: codec.PacketData, Body: msg})
	conn.Write(dataPkt)
	t.Logf("→ Data cmd=2050")

	// 等 handler
	done := make(chan struct{})
	go func() {
		handlerCalled.Wait()
		close(done)
	}()
	select {
	case <-done:
		t.Logf("✓ handler called")
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: handler not called")
	}

	t.Logf("✓ E2E TCP→Gate→Dispatcher→Handler 链路通过")
}

// tcpConnAdapter 适配 net.Conn 为 conn.Connection
type tcpConnAdapter struct{ net.Conn }

func (c *tcpConnAdapter) Send(data []byte)           { c.Conn.Write(data) }
func (c *tcpConnAdapter) RemoteAddr() string          { return c.Conn.RemoteAddr().String() }
func (c *tcpConnAdapter) Done() <-chan struct{}       { return nil }
func (c *tcpConnAdapter) LastRecvUnixNano() int64     { return time.Now().UnixNano() }
func (c *tcpConnAdapter) TouchRecv()                  {}
func (c *tcpConnAdapter) Recv() <-chan *codec.Packet  { return nil }

func (m *Module) handleRawConn(raw net.Conn) {
	c := &tcpConnAdapter{Conn: raw}
	defer func() {
		raw.Close()
		m.poster.Post(func() {
			m.sessions.OnDisconnect(c)
			m.pending.DeleteByConn(c)
		})
	}()
	for {
		hdr := make([]byte, 4)
		if _, err := io.ReadFull(raw, hdr); err != nil {
			return
		}
		bodyLen := int(hdr[1])<<16 | int(hdr[2])<<8 | int(hdr[3])
		frame := make([]byte, 4+bodyLen)
		copy(frame[:4], hdr)
		if bodyLen > 0 {
			if _, err := io.ReadFull(raw, frame[4:]); err != nil {
				return
			}
		}
		pkt, err := codec.DecodePacket(frame)
		if err != nil {
			continue
		}
		pktCopy := pkt
		m.poster.Post(func() {
			_ = m.dispatcher.HandlePacket(c, &pktCopy)
		})
	}
}

type syncPoster3 struct {
	mu  sync.Mutex
	fns []func()
}

func (p *syncPoster3) Post(fn func()) {
	p.mu.Lock()
	p.fns = append(p.fns, fn)
	p.mu.Unlock()
}

func (p *syncPoster3) drain() {
	p.mu.Lock()
	fns := p.fns
	p.fns = nil
	p.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

var _ corerpc.Poster = (*syncPoster3)(nil)

type e2eLobbyHandler struct {
	onClaim func(req *handlerpb.CS_ClaimReward_Req, reply corerpc.Reply[*handlerpb.SC_ClaimReward_Rsp])
}

func (h *e2eLobbyHandler) ClaimReward(ctx corerpc.Ctx, req *handlerpb.CS_ClaimReward_Req, reply corerpc.Reply[*handlerpb.SC_ClaimReward_Rsp]) {
	if h.onClaim != nil {
		h.onClaim(req, reply)
	}
}
func (h *e2eLobbyHandler) SyncPos(ctx corerpc.Ctx, ntf *handlerpb.CS_SyncPos_Ntf) {}
