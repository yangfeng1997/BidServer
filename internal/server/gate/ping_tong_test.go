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
	corerpc "project/internal/core/rpc"
	"project/internal/core/session"
	"project/internal/server/lobby"
	"project/internal/server/routeragent"
	handlerpb "project/protocol/handler"
)

type frameTransport struct {
	ra   *routeragent.Module
	from *routeragent.UDSConn
}

func (t frameTransport) SendFrame(target corerpc.Target, header corerpc.Header, body []byte) error {
	wire := routeragent.RPCWireHeader{
		SeqID:       header.SeqID,
		ServerType:  header.ServerType,
		RoutingMode: uint8(routeragent.RoutingModeHash),
		DeadlineMs:  header.DeadlineMs,
		FromNodeID:  header.FromNodeID,
		RoutingKey:  header.RoutingKey,
		Route:       header.Route,
	}
	if target.Mode == corerpc.RoutingDirect {
		wire.RoutingMode = uint8(routeragent.RoutingModeDirect)
		wire.RoutingKey = ""
		if target.NodeID != 0 {
			wire.RoutingKey = u32toa(target.NodeID)
		}
	}
	t.ra.RouteFrame(t.from, routeragent.Frame{Type: routeragent.FrameRpcRequest, Header: routeragent.EncodeRPCWireHeader(wire), Body: body})
	return nil
}

func TestPingTongClientGateLobbyRoundTrip(t *testing.T) {
	gateNodeID := nodeid.Encode(1, 1, 0).Uint32()
	lobbyNodeID := nodeid.Encode(1, 2, 0).Uint32()

	ra := routeragent.NewModule()
	ra.SetListenAddr("127.0.0.1:7100")
	gateConn := routeragent.NewTestUDSConn("gate")
	lobbyConn := routeragent.NewTestUDSConn("lobby")
	ra.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateNodeID, RAAddr: "local"}, 1)
	ra.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyNodeID, RAAddr: "local"}, 2)
	ra.RegisterConn(gateNodeID, gateConn)
	ra.RegisterConn(lobbyNodeID, lobbyConn)

	gateMod := NewModule()
	gateMod.Set(app.NewBaseApp(nil, false, false, "", "1.1.0", nil, nil))
	gateMod.sessions = session.NewSessionManager()
	clientConn := &testConn{addr: "client:1"}
	sess := gateMod.sessions.OnConnect(clientConn)
	gateMod.rpcCore = corerpc.New(frameTransport{ra: ra, from: gateConn})

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
		head, err := routeragent.DecodeRPCWireHeader(frame.Header)
		if err != nil {
			t.Fatalf("decode response head: %v", err)
		}
		gateMod.rpcCore.OnResponse(head.SeqID, frame.Body, errcode.ErrCode(head.ErrCode))
	case <-time.After(time.Second):
		t.Fatal("gate did not receive tong frame")
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
	var rsp handlerpb.SC_Tong_Rsp
	if err := proto.Unmarshal(msg.Body, &rsp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if rsp.GetText() != "tong:ping" {
		t.Fatalf("response text=%q, want tong:ping", rsp.GetText())
	}
}

func routerEntry(cmdID uint32) dispatcher.RouteEntry {
	return dispatcher.RouteEntry{CmdID: cmdID, ServerType: 2, Route: "LobbyHandler/Ping", RspCmdID: 2055}
}

type lobbyFrameSender struct {
	ra   *routeragent.Module
	from *routeragent.UDSConn
}

func (s lobbyFrameSender) Connect() error { return nil }

func (s lobbyFrameSender) Close() error { return nil }

func (s lobbyFrameSender) Send(frame routeragent.Frame) error {
	s.ra.RouteFrame(s.from, frame)
	return nil
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
