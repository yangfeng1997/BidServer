package main

import (
	"fmt"
	"testing"
	"time"

	"project/internal/core/codec"
	"project/internal/core/dispatcher"
	"project/internal/core/errcode"
	corerpc "project/internal/core/rpc"
	"project/internal/core/session"
	"project/internal/routeragent"
	genhandler "project/protocol/gen/handler"
	handlerpb "project/protocol/handler"
)

// ── RPC Core 往返 ──

func BenchmarkRPCCallOnResponse(b *testing.B) {
	trans := &benchTransport{}
	p := &benchPoster{}
	core := corerpc.New(trans, corerpc.WithPoster(p))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		core.Call(corerpc.Target{ServerType: 2}, "T/H", []byte("b"), corerpc.Background(),
			func(payload []byte, code errcode.ErrCode) { _ = payload })
		core.OnResponse(uint64(i+1), []byte("r"), 0)
		p.drain()
	}
}

// ── Dispatcher 分发 ──

func BenchmarkDispatcherDispatch(b *testing.B) {
	d := dispatcher.New(1)
	d.RegisterRoute(2050, dispatcher.RouteEntry{CmdID: 2050, ServerType: 1, Route: "Test", RspCmdID: 2051})
	genhandler.RegisterLobbyHandler(d, &benchLobbyHandler{})
	sess := &session.Session{ID: "s", ConnID: "c", Authed: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.Dispatch(sess, &codec.Message{CmdID: 2050, SeqID: 1, Body: []byte{0x08, 0x01}})
	}
}

// ── Codec 完整帧 ──

func BenchmarkCodecFullPacket(b *testing.B) {
	pkt := codec.Packet{Type: codec.PacketData}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg, _ := codec.EncodeMessage(codec.Message{Type: codec.MessageRequest, SeqID: 1, CmdID: 2050, Body: []byte{0x08, 0x01}})
		pkt.Body = msg
		data, _ := codec.EncodePacket(pkt)
		dec, _ := codec.DecodePacket(data)
		_, _ = codec.DecodeMessage(dec.Body)
	}
}

// ── RA 同机路由转发 ──

func BenchmarkRARouteFrame(b *testing.B) {
	q := &benchPoster{}
	ra := routeragent.NewModuleForTest(q.Post)
	ra.AfterInit()
	defer ra.BeforeStop()

	g, l := newUDSConnPair("ra")
	gateID, lobbyID := uint32(0x00400001), uint32(0x00400002)
	mt := ra.MemberTable()
	mt.Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: "ra"}, 1)
	mt.Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: "ra"}, 2)
	ra.RegisterConn(gateID, g)
	ra.RegisterConn(lobbyID, l)

	reqHead := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
		SeqID: 1, ServerType: 2, RoutingMode: uint8(routeragent.RoutingModeDirect),
		RoutingKey: fmt.Sprintf("%d", lobbyID), Route: "Test",
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Post(func() { ra.RouteFrame(g, routeragent.Frame{Type: routeragent.FrameRpcRequest, Header: reqHead, Body: []byte{0x08, 0x01}}) })
		q.drain()
		select {
		case <-l.Recv():
		default:
		}
	}
}

// ── 同机往返（单 RA）──

func BenchmarkSameRARoundTrip(b *testing.B) {
	q := &benchPoster{}
	ra := routeragent.NewModuleForTest(q.Post)
	ra.AfterInit()
	defer ra.BeforeStop()

	g, l := newUDSConnPair("ra")
	gateID, lobbyID := uint32(0x00400001), uint32(0x00400002)
	mt := ra.MemberTable()
	mt.Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: "ra"}, 1)
	mt.Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: "ra"}, 2)
	ra.RegisterConn(gateID, g)
	ra.RegisterConn(lobbyID, l)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reqH := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
			SeqID: uint64(i + 1), ServerType: 2,
			RoutingMode: uint8(routeragent.RoutingModeDirect),
			RoutingKey:  fmt.Sprintf("%d", lobbyID), Route: "Test",
		})
		q.Post(func() { ra.RouteFrame(g, routeragent.Frame{Type: routeragent.FrameRpcRequest, Header: reqH, Body: []byte{0x08, 0x01}}) })
		q.drain()
		select {
		case lf := <-l.Recv():
			lh, _ := routeragent.DecodeRPCWireHeader(lf.Header)
			respH := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{SeqID: lh.SeqID, FromNodeID: gateID})
			q.Post(func() { ra.RouteFrame(l, routeragent.Frame{Type: routeragent.FrameRpcResponse, Header: respH, Body: []byte{0x10, 0x00}}) })
			q.drain()
			select {
			case <-g.Recv():
			default:
			}
		default:
		}
	}
}

// ── 跨 RA 单向转发 ──

func BenchmarkCrossRASend(b *testing.B) {
	qA, qB := &benchPoster{}, &benchPoster{}
	raA, raB := newRA(qA, "a:1"), newRA(qB, "b:1")
	defer raA.BeforeStop()
	defer raB.BeforeStop()

	g := routeragent.NewTestUDSConn("a:1")
	l := routeragent.NewTestUDSConn("b:1")
	gateID, lobbyID := uint32(0x00400001), uint32(0x00400002)
	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: "a:1"}, 1)
	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: "b:1"}, 2)
	raA.RegisterConn(gateID, g)
	raB.RegisterConn(lobbyID, l)

	// 用 channel pipe 模仿 TCP 连接
	chAB := make(chan []byte, 64)
	raA.PeerMgr().Attach("b:1", routeragent.NewTCPPeerLink(&chWriter{ch: chAB}, "b:1"))
	raA.PeerMgr().SetState("b:1", routeragent.PeerConnected)

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case data := <-chAB:
				qB.Post(func() { deliverToRA(raB, data) })
				qB.drain()
			case <-done:
				return
			}
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reqH := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
			SeqID: uint64(i + 1), ServerType: 2,
			RoutingMode: uint8(routeragent.RoutingModeDirect),
			RoutingKey:  fmt.Sprintf("%d", lobbyID), Route: "Test",
		})
		qA.Post(func() { raA.RouteFrame(g, routeragent.Frame{Type: routeragent.FrameRpcRequest, Header: reqH, Body: []byte{0x08, 0x01}}) })
		qA.drain()
		qB.drain()
		select {
		case <-l.Recv():
		default:
		}
	}
}

// ── 跨 RA 往返 ──

func BenchmarkCrossRARoundTrip(b *testing.B) {
	qA, qB := &benchPoster{}, &benchPoster{}
	raA, raB := newRA(qA, "a:2"), newRA(qB, "b:2")
	defer raA.BeforeStop()
	defer raB.BeforeStop()

	g := routeragent.NewTestUDSConn("a:2")
	l := routeragent.NewTestUDSConn("b:2")
	gateID, lobbyID := uint32(0x00400001), uint32(0x00400002)
	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: "a:2"}, 1)
	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: "b:2"}, 2)
	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: "b:2"}, 2)
	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: "a:2"}, 1)
	raA.RegisterConn(gateID, g)
	raB.RegisterConn(lobbyID, l)

	chAB := make(chan []byte, 64)
	chBA := make(chan []byte, 64)
	raA.PeerMgr().Attach("b:2", routeragent.NewTCPPeerLink(&chWriter{ch: chAB}, "b:2"))
	raB.PeerMgr().Attach("a:2", routeragent.NewTCPPeerLink(&chWriter{ch: chBA}, "a:2"))
	raA.PeerMgr().SetState("b:2", routeragent.PeerConnected)
	raB.PeerMgr().SetState("a:2", routeragent.PeerConnected)

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case data := <-chAB:
				qB.Post(func() { deliverToRA(raB, data) })
				qB.drain()
			case <-done:
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case data := <-chBA:
				qA.Post(func() { deliverToRA(raA, data) })
				qA.drain()
			case <-done:
				return
			}
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reqH := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
			SeqID: uint64(i + 1), ServerType: 2,
			RoutingMode: uint8(routeragent.RoutingModeDirect),
			RoutingKey:  fmt.Sprintf("%d", lobbyID), Route: "Test",
		})
		qA.Post(func() { raA.RouteFrame(g, routeragent.Frame{Type: routeragent.FrameRpcRequest, Header: reqH, Body: []byte{0x08, 0x01}}) })
		qA.drain()
		qB.drain()
		select {
		case lf := <-l.Recv():
			lh, _ := routeragent.DecodeRPCWireHeader(lf.Header)
			respH := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{SeqID: lh.SeqID, FromNodeID: gateID})
			qB.Post(func() { raB.RouteFrame(l, routeragent.Frame{Type: routeragent.FrameRpcResponse, Header: respH, Body: []byte{0x10, 0x00}}) })
			qB.drain()
			qA.drain()
			select {
			case <-g.Recv():
			default:
			}
		default:
		}
	}
}

// ── helper types ──

func newRA(q *benchPoster, addr string) *routeragent.Module {
	m := routeragent.NewModuleForTest(q.Post)
	m.AfterInit()
	m.SetListenAddr(addr)
	m.PeerMgr().SetListenAddr(addr)
	return m
}

func newUDSConnPair(addr string) (*routeragent.UDSConn, *routeragent.UDSConn) {
	return routeragent.NewTestUDSConn(addr), routeragent.NewTestUDSConn(addr)
}

type chWriter struct{ ch chan []byte }

func (c *chWriter) Read(b []byte) (int, error)        { return 0, nil }
func (c *chWriter) Write(b []byte) (int, error) {
	d := make([]byte, len(b))
	copy(d, b)
	c.ch <- d
	return len(b), nil
}
func (c *chWriter) Close() error                       { return nil }
func (c *chWriter) SetReadDeadline(time.Time) error    { return nil }
func (c *chWriter) SetWriteDeadline(time.Time) error   { return nil }

type benchTransport struct{ calls int }

func (t *benchTransport) SendFrame(target corerpc.Target, header corerpc.Header, body []byte) error {
	t.calls++
	return nil
}

type benchPoster struct{ fns []func() }

func (p *benchPoster) Post(fn func()) { p.fns = append(p.fns, fn) }
func (p *benchPoster) drain() {
	for _, fn := range p.fns {
		fn()
	}
	p.fns = p.fns[:0]
}

type benchLobbyHandler struct{}

func (h *benchLobbyHandler) ClaimReward(ctx corerpc.Ctx, req *handlerpb.CS_ClaimReward_Req, reply corerpc.Reply[*handlerpb.SC_ClaimReward_Rsp]) {
	reply(&handlerpb.SC_ClaimReward_Rsp{}, nil)
}
func (h *benchLobbyHandler) SyncPos(ctx corerpc.Ctx, ntf *handlerpb.CS_SyncPos_Ntf) {}
