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
	m.PeerMgr().Attach("peer", link)
	m.routeFrame(&UDSConn{remoteAddr: "local"}, Frame{Type: FrameRpcRequest, Body: EncodeRouteBody(nodeID, []byte("hi"))})
	select {
	case <-link.recvCh:
	default:
	}
}
