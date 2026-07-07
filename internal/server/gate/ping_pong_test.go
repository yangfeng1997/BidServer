package gate

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"project/internal/core/app"
	"project/internal/core/codec"
	"project/internal/core/dispatcher"
	"project/internal/core/errcode"
	"project/internal/core/nodeid"
	ragentagent "project/internal/core/ragent/agent"
	ragentwire "project/internal/core/ragent/wire"
	corerpc "project/internal/core/rpc"
	"project/internal/core/session"
	"project/internal/server/lobby"
	handlerpb "project/protocol/handler"
)

type testConn struct {
	addr string
	sent [][]byte
}

func (c *testConn) Send(data []byte) {
	c.sent = append(c.sent, append([]byte(nil), data...))
}
func (c *testConn) Close() error              { return nil }
func (c *testConn) RemoteAddr() string        { return c.addr }
func (c *testConn) Done() <-chan struct{}     { return nil }
func (c *testConn) LastRecvUnixNano() int64   { return 0 }
func (c *testConn) TouchRecv()                {}
func (c *testConn) Recv() <-chan codec.Packet { return nil }

type frameTransport struct {
	ra   *ragentagent.Runtime
	from *ragentagent.UDSConn
}

func (t frameTransport) SendFrame(target corerpc.Target, header corerpc.Header, body []byte) error {
	wire := ragentwire.RPCWireHeader{
		SeqID:       header.SeqID,
		ServerType:  header.ServerType,
		RoutingMode: uint8(ragentwire.RoutingModeHash),
		DeadlineMs:  header.DeadlineMs,
		SrcNodeID:   header.SrcNodeID,
		DestNodeID:  header.DestNodeID,
		RoutingKey:  header.RoutingKey,
		Route:       header.Route,
	}
	if target.Mode == corerpc.RoutingDirect {
		wire.RoutingMode = uint8(ragentwire.RoutingModeDirect)
		wire.RoutingKey = ""
		if target.NodeID != 0 {
			wire.DestNodeID = target.NodeID
			wire.RoutingKey = u32toa(target.NodeID)
		}
	}
	t.ra.RouteFrame(t.from, ragentwire.Frame{Type: ragentwire.FrameRpcRequest, Header: ragentwire.EncodeRPCWireHeader(wire), Body: body})
	return nil
}

func TestPingPongClientGateLobbyRoundTrip(t *testing.T) {
	gateNodeID := nodeid.Encode(1, 1, 0).Uint32()
	lobbyNodeID := nodeid.Encode(1, 2, 0).Uint32()

	ra := ragentagent.NewRuntime()
	ra.SetListenAddr("127.0.0.1:7100")
	gateConn := ragentagent.NewTestUDSConn("gate")
	lobbyConn := ragentagent.NewTestUDSConn("lobby")
	ra.MemberTable().Upsert(ragentagent.NodeInfo{NodeID: gateNodeID, RAAddr: "local"}, 1)
	ra.MemberTable().Upsert(ragentagent.NodeInfo{NodeID: lobbyNodeID, RAAddr: "local"}, 2)
	ra.RegisterConn(gateNodeID, gateConn)
	ra.RegisterConn(lobbyNodeID, lobbyConn)

	gateMod := NewModule()
	gateMod.Set(app.NewBaseApp(nil, false, false, "", "1.1.0", nil, nil))
	gateMod.sessions = session.NewSessionManager()
	clientConn := &testConn{addr: "client:1"}
	sess := gateMod.sessions.OnConnect(clientConn)
	gateMod.rpcCore = corerpc.New(frameTransport{ra: ra, from: gateConn})
	defer gateMod.rpcCore.Close()

	lobbyMod := lobby.NewModuleForTest()
	lobbyMod.SetClient(lobbyFrameSender{ra: ra, from: lobbyConn})

	reqPayload, err := proto.Marshal(&handlerpb.CS_Ping_Req{Text: "ping"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if err := gateMod.forwardToBackend(sess, &codec.Message{Type: codec.MessageRequest, SeqID: 7, CmdID: 2054, Body: reqPayload}, routerEntry(2054)); err != nil {
		t.Fatalf("forward ping: %v", err)
	}
	select {
	case frame := <-lobbyConn.Recv():
		lobbyMod.HandleRagentFrame(frame)
	case <-time.After(time.Second):
		t.Fatal("lobby did not receive ping frame")
	}
	select {
	case frame := <-gateConn.Recv():
		head, err := ragentwire.DecodeRPCWireHeader(frame.Header)
		if err != nil {
			t.Fatalf("decode response head: %v", err)
		}
		gateMod.rpcCore.OnResponse(head.SeqID, frame.Body, errcode.ErrCode(head.ErrCode))
	case <-time.After(time.Second):
		t.Fatal("gate did not receive pong frame")
	}
	if len(clientConn.sent) != 1 {
		t.Fatalf("client packets=%d, want 1", len(clientConn.sent))
	}
	pkt, err := codec.DecodePacket(clientConn.sent[0])
	if err != nil {
		t.Fatalf("decode packet: %v", err)
	}
	msg, err := codec.DecodeMessage(pkt.Body)
	if err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if msg.Type != codec.MessageResponse || msg.SeqID != 7 || msg.CmdID != 2055 || msg.ErrCode != errcode.OK {
		t.Fatalf("unexpected response message: %+v", msg)
	}
	var rsp handlerpb.SC_Pong_Rsp
	if err := proto.Unmarshal(msg.Body, &rsp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if rsp.GetText() != "pong:ping" {
		t.Fatalf("response text=%q, want pong:ping", rsp.GetText())
	}
}

func routerEntry(cmdID uint32) dispatcher.RouteEntry {
	return dispatcher.RouteEntry{CmdID: cmdID, ServerType: 2, Route: "LobbyHandler/Ping", RspCmdID: 2055}
}

type lobbyFrameSender struct {
	ra   *ragentagent.Runtime
	from *ragentagent.UDSConn
}

func (s lobbyFrameSender) Connect() error { return nil }

func (s lobbyFrameSender) Close() error { return nil }

func (s lobbyFrameSender) Send(frame ragentwire.Frame) error {
	s.ra.RouteFrame(s.from, frame)
	return nil
}

func (s lobbyFrameSender) SendRPCFrame(frameType ragentwire.FrameType, head ragentwire.RPCWireHeader, body []byte) error {
	return s.Send(ragentwire.Frame{Type: frameType, Header: ragentwire.EncodeRPCWireHeader(head), Body: body})
}

func u32toa(v uint32) string {
	var buf [10]byte
	i := len(buf)
	if v == 0 {
		return "0"
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
