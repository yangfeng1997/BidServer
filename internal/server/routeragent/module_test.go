package routeragent

import (
	"encoding/binary"
	"testing"

	"project/internal/core/errcode"
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

func TestPeerRequestStampsSrcDestAndRoutesDirect(t *testing.T) {
	m := NewModule()
	source := &UDSConn{remoteAddr: "unix://gate", done: make(chan struct{}), sendCh: make(chan Frame, 1), recvCh: make(chan Frame, 1)}
	link := &UDSConn{remoteAddr: "peer", done: make(chan struct{}), sendCh: make(chan Frame, 1), recvCh: make(chan Frame, 1)}
	head := RPCWireHeader{
		SeqID:       7,
		ServerType:  2,
		RoutingMode: uint8(RoutingModeHash),
		SrcNodeID:   0x01010101,
		RoutingKey:  "client-1",
		Route:       "LobbyHandler/Ping",
	}

	if err := m.sendPeerOutbound(link, peerOutbound{
		source:       source,
		frame:        Frame{Type: FrameRpcRequest, Header: EncodeRPCWireHeader(head), Body: []byte("hi")},
		head:         head,
		origSeqID:    head.SeqID,
		targetNodeID: 0x01020202,
		prepareRPC:   true,
	}); err != nil {
		t.Fatalf("send peer outbound: %v", err)
	}

	var remoteSeq uint64
	select {
	case got := <-link.sendCh:
		gotHead, err := DecodeRPCWireHeader(got.Header)
		if err != nil {
			t.Fatalf("decode forwarded head: %v", err)
		}
		if gotHead.SrcNodeID != head.SrcNodeID {
			t.Fatalf("src_node=%d, want %d", gotHead.SrcNodeID, head.SrcNodeID)
		}
		if gotHead.DestNodeID != 0x01020202 {
			t.Fatalf("dest_node=%d, want %d", gotHead.DestNodeID, uint32(0x01020202))
		}
		if gotHead.RoutingMode != uint8(RoutingModeDirect) || gotHead.RoutingKey != "16908802" {
			t.Fatalf("route mode/key=%d/%q, want direct target", gotHead.RoutingMode, gotHead.RoutingKey)
		}
		if gotHead.SeqID == head.SeqID || gotHead.SeqID == 0 {
			t.Fatalf("remote seq=%d should be allocated and non-zero", gotHead.SeqID)
		}
		remoteSeq = gotHead.SeqID
	default:
		t.Fatal("expected frame to be sent to peer")
	}

	entry := m.RemoteSeqMap().PopPublic(remoteSeq)
	if entry == nil || entry.UDSConn != source || entry.OrigSeqID != head.SeqID {
		t.Fatalf("remote seq mapping invalid: %+v", entry)
	}
}

func TestHandlePeerFrameRequestRoutesByDestNode(t *testing.T) {
	m := NewModule()
	targetNodeID := uint32(0x01020202)
	fromNodeID := uint32(0x01010101)
	local := &UDSConn{remoteAddr: "unix://lobby", done: make(chan struct{}), sendCh: make(chan Frame, 1), recvCh: make(chan Frame, 1)}
	m.RegisterConn(targetNodeID, local)
	head := RPCWireHeader{SeqID: 3, ServerType: 2, RoutingMode: uint8(RoutingModeDirect), SrcNodeID: fromNodeID, DestNodeID: targetNodeID, RoutingKey: "stale-key", Route: "LobbyHandler/Ping"}

	m.handlePeerFrame(Frame{Type: FrameRpcRequest, Header: EncodeRPCWireHeader(head), Body: []byte("hi")})

	select {
	case got := <-local.sendCh:
		gotHead, err := DecodeRPCWireHeader(got.Header)
		if err != nil {
			t.Fatalf("decode local head: %v", err)
		}
		if gotHead.SrcNodeID != fromNodeID || gotHead.DestNodeID != targetNodeID {
			t.Fatalf("src/dest=%d/%d, want %d/%d", gotHead.SrcNodeID, gotHead.DestNodeID, fromNodeID, targetNodeID)
		}
		if string(got.Body) != "hi" {
			t.Fatalf("body=%q, want hi", string(got.Body))
		}
	default:
		t.Fatal("expected peer request to be delivered by routing key")
	}
}

func TestRouteMissReturnsErrorForPeerRequest(t *testing.T) {
	m := NewModule()
	source := &UDSConn{remoteAddr: "unix://gate", done: make(chan struct{}), sendCh: make(chan Frame, 1), recvCh: make(chan Frame, 1)}
	head := RPCWireHeader{SeqID: 9, ServerType: 2, RoutingMode: uint8(RoutingModeDirect), SrcNodeID: 0x01010101, RoutingKey: "16908802", Route: "LobbyHandler/Ping"}

	m.handlePeerFrame(Frame{Type: FrameRpcRequest, Header: EncodeRPCWireHeader(head), Body: []byte("hi")})

	select {
	case <-source.sendCh:
		t.Fatal("unregistered source must not receive response")
	default:
	}
	if m.metrics.RouteMiss.Load() == 0 {
		t.Fatal("expected route miss to be counted")
	}
}

func TestPeerResponseMapsRemoteSeqBackToCaller(t *testing.T) {
	m := NewModule()
	source := &UDSConn{remoteAddr: "unix://gate", done: make(chan struct{}), sendCh: make(chan Frame, 1), recvCh: make(chan Frame, 1)}
	remoteSeq := m.RemoteSeqMap().Alloc(source, 11)
	head := RPCWireHeader{SeqID: remoteSeq, SrcNodeID: 0x01020202, DestNodeID: 0x01010101, ErrCode: uint32(errcode.OK), Route: "LobbyHandler/Ping"}

	m.handlePeerFrame(Frame{Type: FrameRpcResponse, Header: EncodeRPCWireHeader(head), Body: []byte("tong")})

	select {
	case got := <-source.sendCh:
		gotHead, err := DecodeRPCWireHeader(got.Header)
		if err != nil {
			t.Fatalf("decode response head: %v", err)
		}
		if gotHead.SeqID != 11 || gotHead.SrcNodeID != 0x01020202 || gotHead.DestNodeID != 0x01010101 || errcode.ErrCode(gotHead.ErrCode) != errcode.OK {
			t.Fatalf("unexpected response head: %+v", gotHead)
		}
		if string(got.Body) != "tong" {
			t.Fatalf("body=%q, want tong", string(got.Body))
		}
	default:
		t.Fatal("expected peer response to be sent to original UDS conn")
	}
}

func TestFailOutboundSendsOriginalSeqError(t *testing.T) {
	source := &UDSConn{remoteAddr: "unix://gate", done: make(chan struct{}), sendCh: make(chan Frame, 1), recvCh: make(chan Frame, 1)}
	head := RPCWireHeader{SeqID: 11, SrcNodeID: 0x01010101, Route: "LobbyHandler/Ping"}
	m := NewModule()

	m.failOutbound(peerOutbound{source: source, frame: Frame{Type: FrameRpcRequest}, head: head, origSeqID: head.SeqID}, errcode.ERR_INTERNAL)

	select {
	case got := <-source.sendCh:
		gotHead, err := DecodeRPCWireHeader(got.Header)
		if err != nil {
			t.Fatalf("decode error response head: %v", err)
		}
		if gotHead.SeqID != head.SeqID || gotHead.SrcNodeID != 0 || gotHead.DestNodeID != head.SrcNodeID || errcode.ErrCode(gotHead.ErrCode) != errcode.ERR_INTERNAL {
			t.Fatalf("unexpected error head: %+v", gotHead)
		}
	default:
		t.Fatal("expected failOutbound response")
	}
}
