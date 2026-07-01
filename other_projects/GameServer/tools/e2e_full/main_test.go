package main

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"project/internal/routeragent"
)

// TestSameRA 验证：同一 RA 内两个本地进程的 RPC 往返
func TestSameRA(t *testing.T) {
	q := &syncQueue{}
	ra := routeragent.NewModuleForTest(q.Post)
	ra.AfterInit()
	defer ra.BeforeStop()

	gateConn := routeragent.NewTestUDSConn("ra-local")
	lobbyConn := routeragent.NewTestUDSConn("ra-local")
	gateNodeID := uint32(0x00400001)
	lobbyNodeID := uint32(0x00400002)

	mt := ra.MemberTable()
	mt.Upsert(routeragent.NodeInfo{NodeID: gateNodeID, RAAddr: "ra-local"}, 1)
	mt.Upsert(routeragent.NodeInfo{NodeID: lobbyNodeID, RAAddr: "ra-local"}, 2)
	ra.RegisterConn(gateNodeID, gateConn)
	ra.RegisterConn(lobbyNodeID, lobbyConn)

	reqHead := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
		SeqID: 1, ServerType: 2, RoutingMode: uint8(routeragent.RoutingModeDirect),
		RoutingKey: fmt.Sprintf("%d", lobbyNodeID), Route: "LobbyHandler/ClaimReward",
	})
	q.Post(func() {
		ra.RouteFrame(gateConn, routeragent.Frame{
			Type: routeragent.FrameRpcRequest, Header: reqHead, Body: []byte{0x08, 0x01},
		})
	})
	q.drain()

	select {
	case f := <-lobbyConn.Recv():
		h, _ := routeragent.DecodeRPCWireHeader(f.Header)
		t.Logf("  lobby: seq=%d route=%s ✓", h.SeqID, h.Route)
		respHead := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
			SeqID: h.SeqID, FromNodeID: gateNodeID,
		})
		q.Post(func() {
			ra.RouteFrame(lobbyConn, routeragent.Frame{
				Type: routeragent.FrameRpcResponse, Header: respHead, Body: []byte{0x10, 0x00},
			})
		})
		q.drain()
		select {
		case r := <-gateConn.Recv():
			rh, _ := routeragent.DecodeRPCWireHeader(r.Header)
			t.Logf("  gate: seq=%d ✓", rh.SeqID)
			t.Logf("✓ 同机 RA 本地转发通过")
		case <-time.After(time.Second):
			t.Fatal("timeout")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

// TestCrossRA 验证：两台 RA 之间跨机 RPC 往返
func TestCrossRA(t *testing.T) {
	qA := &syncQueue{}
	qB := &syncQueue{}
	raA := routeragent.NewModuleForTest(qA.Post)
	raB := routeragent.NewModuleForTest(qB.Post)
	raA.AfterInit()
	raB.AfterInit()
	defer raA.BeforeStop()
	defer raB.BeforeStop()

	raA.SetListenAddr("10.0.0.1:8000")
	raB.SetListenAddr("10.0.0.2:8000")
	raA.PeerMgr().SetListenAddr("10.0.0.1:8000")
	raB.PeerMgr().SetListenAddr("10.0.0.2:8000")

	gateUDS := routeragent.NewTestUDSConn("10.0.0.1:8000")
	lobbyUDS := routeragent.NewTestUDSConn("10.0.0.2:8000")
	gateNodeID := uint32(0x00400001)
	lobbyNodeID := uint32(0x00400002)

	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateNodeID, RAAddr: "10.0.0.1:8000"}, 1)
	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyNodeID, RAAddr: "10.0.0.2:8000"}, 2)
	raA.RegisterConn(gateNodeID, gateUDS)

	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyNodeID, RAAddr: "10.0.0.2:8000"}, 2)
	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateNodeID, RAAddr: "10.0.0.1:8000"}, 1)
	raB.RegisterConn(lobbyNodeID, lobbyUDS)

	// in-memory TCP 管道
	raA2B, raB2A := net.Pipe()

	// 在 raA 侧：启动 peer writeLoop，写数据到 raA2B
	plA := newTestTCPLink(raA2B, "10.0.0.2:8000")
	raA.PeerMgr().Attach("10.0.0.2:8000", plA)
	raA.PeerMgr().SetState("10.0.0.2:8000", routeragent.PeerConnected)

	// 在 raB 侧：启动 peer writeLoop，写数据到 raB2A
	plB := newTestTCPLink(raB2A, "10.0.0.1:8000")
	raB.PeerMgr().Attach("10.0.0.1:8000", plB)
	raB.PeerMgr().SetState("10.0.0.1:8000", routeragent.PeerConnected)

	// TCP 读循环：raA 的 peer 写 raA2B → 管道 → 另一端 raB2A 可读 → 发给 raB
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := readRawFrame(raB2A)
			if err != nil {
				continue
			}
			qB.Post(func() { deliverToRA(raB, data) })
			qB.drain()
		}
	}()
	// TCP 读循环：raB 的 peer 写 raB2A → 管道 → 另一端 raA2B 可读 → 发给 raA
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := readRawFrame(raA2B)
			if err != nil {
				continue
			}
			qA.Post(func() { deliverToRA(raA, data) })
			qA.drain()
		}
	}()

	// ── gate → raA → TCP → raB → lobby ──
	reqHead := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
		SeqID: 1, ServerType: 2, RoutingMode: uint8(routeragent.RoutingModeDirect),
		RoutingKey: fmt.Sprintf("%d", lobbyNodeID), Route: "LobbyHandler/ClaimReward",
	})

	qA.Post(func() {
		raA.RouteFrame(gateUDS, routeragent.Frame{
			Type: routeragent.FrameRpcRequest, Header: reqHead, Body: []byte{0x08, 0x01},
		})
	})
	// 多次 drain 确保链路传递
	for i := 0; i < 5; i++ {
		qA.drain()
		time.Sleep(20 * time.Millisecond)
		qB.drain()
		time.Sleep(20 * time.Millisecond)
	}

	select {
	case lobbyFrame := <-lobbyUDS.Recv():
		lobbyH, _ := routeragent.DecodeRPCWireHeader(lobbyFrame.Header)
		t.Logf("← raB→lobby(跨机): seq=%d route=%s ✓", lobbyH.SeqID, lobbyH.Route)

		// 回包
		respHead := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
			SeqID: lobbyH.SeqID, FromNodeID: gateNodeID,
		})
		qB.Post(func() {
			raB.RouteFrame(lobbyUDS, routeragent.Frame{
				Type: routeragent.FrameRpcResponse, Header: respHead, Body: []byte{0x10, 0x00},
			})
		})
		for i := 0; i < 5; i++ {
			qB.drain()
			time.Sleep(20 * time.Millisecond)
			qA.drain()
			time.Sleep(20 * time.Millisecond)
		}

		select {
		case gateResp := <-gateUDS.Recv():
			rspH, _ := routeragent.DecodeRPCWireHeader(gateResp.Header)
			t.Logf("→ gate: seq=%d ✓", rspH.SeqID)
			t.Logf("")
			t.Logf("╔══════════════════════════════════╗")
			t.Logf("║  ✓ 跨 RA 链路通过                 ║")
			t.Logf("║  gate→RA-A→TCP→RA-B→lobby       ║")
			t.Logf("║  lobby→RA-B→TCP→RA-A→gate       ║")
			t.Logf("╚══════════════════════════════════╝")
		case <-time.After(2 * time.Second):
			t.Fatal("timeout: gate no cross-RA response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: lobby no cross-RA request (raA forwardRPC may not reach raB)")
	}
}

// newTestTCPLink 创建带 writeLoop 的 peer 连接（集成测试用）
func newTestTCPLink(raw io.ReadWriter, addr string) routeragent.PeerLink {
	return routeragent.NewTCPPeerLink(&rwConn{rw: raw}, addr)
}

// rwConn 将 io.ReadWriter 适配为 net.Conn-like interface
type rwConn struct {
	rw   io.ReadWriter
	addr string
}

func (c *rwConn) Read(b []byte) (int, error)         { return c.rw.Read(b) }
func (c *rwConn) Write(b []byte) (int, error)        { return c.rw.Write(b) }
func (c *rwConn) Close() error                        { return nil }
func (c *rwConn) SetReadDeadline(_ time.Time) error   { return nil }
func (c *rwConn) SetWriteDeadline(_ time.Time) error  { return nil }

// deliverToRA 模拟对端 RA 收到 TCP 帧后的处理
func deliverToRA(m *routeragent.Module, data []byte) {
	f, err := routeragent.DecodeFrame(data)
	if err != nil {
		return
	}
	switch f.Type {
	case routeragent.FrameRpcResponse:
		head, err := routeragent.DecodeRPCWireHeader(f.Header)
		if err != nil {
			return
		}
		entry := m.RemoteSeqMap().PopPublic(head.SeqID)
		if entry != nil && entry.UDSConn != nil {
			head.SeqID = entry.OrigSeqID
			_ = entry.UDSConn.Send(routeragent.Frame{
				Type:   routeragent.FrameRpcResponse,
				Header: routeragent.EncodeRPCWireHeader(head),
				Body:   f.Body,
			})
		}
	case routeragent.FrameRpcRequest, routeragent.FrameRpcNotify:
		head, _ := routeragent.DecodeRPCWireHeader(f.Header)
		m.DeliverToLocalConn(head.FromNodeID, f)
	}
}

func readRawFrame(r io.Reader) ([]byte, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return nil, err
	}
	length := int(lenBuf[0])<<24 | int(lenBuf[1])<<16 | int(lenBuf[2])<<8 | int(lenBuf[3])
	if length < 3 || length > 1024*1024 {
		return nil, fmt.Errorf("bad frame length %d", length)
	}
	frame := make([]byte, 4+length)
	copy(frame[:4], lenBuf)
	if _, err := io.ReadFull(r, frame[4:]); err != nil {
		return nil, err
	}
	return frame, nil
}

type syncQueue struct {
	mu  sync.Mutex
	fns []func()
}

func (q *syncQueue) Post(fn func()) {
	q.mu.Lock()
	q.fns = append(q.fns, fn)
	q.mu.Unlock()
}

func (q *syncQueue) drain() {
	q.mu.Lock()
	fns := make([]func(), len(q.fns))
	copy(fns, q.fns)
	q.fns = nil
	q.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
