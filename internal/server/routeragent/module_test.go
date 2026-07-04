package routeragent

import (
	"encoding/binary"
	"testing"
)

func TestMemberTable(t *testing.T) {
	mt := NewMemberTable()
	mt.Upsert(NodeInfo{NodeID: 1, RAAddr: "a"}, 2)
	if got, ok := mt.GetByNodeID(1); !ok || got.RAAddr != "a" {
		t.Fatal("member table lookup failed")
	}
}

func TestHandleHandshake(t *testing.T) {
	m := NewModule()
	frame := Frame{Type: FrameHandshake, Body: make([]byte, 4)}
	binary.BigEndian.PutUint32(frame.Body, 0x01020304)
	u := &UDSConn{remoteAddr: "unix://test", done: make(chan struct{}), sendCh: make(chan Frame, 1), recvCh: make(chan Frame, 1)}
	m.handleFrame(u, frame)
	if _, ok := m.MemberTable().GetByNodeID(0x01020304); !ok {
		t.Fatal("handshake should register node")
	}
}

func TestRouteFrame(t *testing.T) {
	m := NewModule()
	nodeID := uint32(0x01020304)
	m.MemberTable().Upsert(NodeInfo{NodeID: nodeID, RAAddr: "peer"}, 2)
	link := &UDSConn{remoteAddr: "peer", done: make(chan struct{}), sendCh: make(chan Frame, 1), recvCh: make(chan Frame, 1)}
	m.PeerMgr().Attach("peer", link, "test")
	head := RPCWireHeader{ServerType: 2, RoutingMode: uint8(RoutingModeDirect), RoutingKey: "16909060", Route: "test"}
	m.routeFrame(&UDSConn{remoteAddr: "local"}, Frame{Type: FrameRpcRequest, Header: EncodeRPCWireHeader(head), Body: []byte("hi")})
	select {
	case <-link.sendCh:
	default:
		t.Fatal("expected frame to be forwarded to peer")
	}
}

func TestRouteFrameUsesRegisteredLocalConn(t *testing.T) {
	m := NewModule()
	nodeID := uint32(0x01020304)
	local := &UDSConn{remoteAddr: "unix://local", done: make(chan struct{}), sendCh: make(chan Frame, 1), recvCh: make(chan Frame, 1)}
	m.MemberTable().Upsert(NodeInfo{NodeID: nodeID, RAAddr: "unix://same-ra"}, 2)
	m.RegisterConn(nodeID, local)
	head := RPCWireHeader{ServerType: 2, RoutingMode: uint8(RoutingModeDirect), RoutingKey: "16909060", Route: "test"}
	m.routeFrame(&UDSConn{remoteAddr: "unix://caller"}, Frame{Type: FrameRpcNotify, Header: EncodeRPCWireHeader(head), Body: []byte("hi")})
	select {
	case got := <-local.sendCh:
		if string(got.Body) != "hi" {
			t.Fatalf("local body=%q, want hi", string(got.Body))
		}
	default:
		t.Fatal("expected frame to be delivered to registered local conn")
	}
}
