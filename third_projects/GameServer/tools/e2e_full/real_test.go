package main

import (
	"sync/atomic"
	"testing"
	"time"

	"project/internal/core/errcode"
	corerpc "project/internal/core/rpc"
	"project/internal/routeragent"
)

// TestCrossRARealSim 真实框架链路模拟
// 使用真正的 rpc.Core.Call()（异步登记回调），RA 路由，lobby 自动回包
// 主循环无 sleep，纯 drain
func TestCrossRARealSim(t *testing.T) {
	qA, qB := &syncQueue{}, &syncQueue{}
	raA, raB := newRAsync(qA, "a"), newRAsync(qB, "b")
	defer raA.BeforeStop()
	defer raB.BeforeStop()

	gateUDS := routeragent.NewTestUDSConn("a")
	lobbyUDS := routeragent.NewTestUDSConn("b")
	gateID, lobbyID := uint32(0x00400001), uint32(0x00400002)

	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: "a"}, 1)
	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: "b"}, 2)
	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: "b"}, 2)
	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: "a"}, 1)
	raA.RegisterConn(gateID, gateUDS)
	raB.RegisterConn(lobbyID, lobbyUDS)

	ab := make(chan []byte, 512)
	ba := make(chan []byte, 512)
	raA.PeerMgr().Attach("b", routeragent.NewTCPPeerLink(&chWriter{ch: ab}, "b"))
	raB.PeerMgr().Attach("a", routeragent.NewTCPPeerLink(&chWriter{ch: ba}, "a"))
	raA.PeerMgr().SetState("b", routeragent.PeerConnected)
	raB.PeerMgr().SetState("a", routeragent.PeerConnected)

	// 构造真实 rpc.Core（Gate 端，Transport = raTransport）
	coreA := corerpc.New(&raTransport{ra: raA, uds: gateUDS, q: qA}, corerpc.WithPoster(qA))

	// Lobby 自动回包
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case lf := <-lobbyUDS.Recv():
				lh, _ := routeragent.DecodeRPCWireHeader(lf.Header)
				qB.Post(func() {
					raB.RouteFrame(lobbyUDS, routeragent.Frame{
						Type:   routeragent.FrameRpcResponse,
						Header: routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
							SeqID: lh.SeqID, FromNodeID: gateID,
						}),
						Body: []byte{0x10, 0x00},
					})
				})
			case <-done:
				return
			}
		}
	}()

	// 持续从 gateUDS 读取响应帧 → OnResponse
	var responses atomic.Int64
	go func() {
		for {
			select {
			case gf := <-gateUDS.Recv():
				gh, _ := routeragent.DecodeRPCWireHeader(gf.Header)
				qA.Post(func() { coreA.OnResponse(gh.SeqID, gf.Body, 0) })
				responses.Add(1)
			case <-done:
				return
			}
		}
	}()

	// 预热
	var preSeq atomic.Uint64
	for i := 0; i < 5000; i++ {
		preSeq.Add(1)
		coreA.Call(corerpc.Target{ServerType: 2, Mode: corerpc.RoutingDirect,
			NodeID: lobbyID}, "Lobby/Test", []byte{0x08, 0x01},
			corerpc.Background(), func([]byte, errcode.ErrCode) {})
		qA.drain(); drainPipe(ab, qB, raB); qB.drain(); drainPipe(ba, qA, raA); qA.drain()
	}
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 10000; i++ {
		qA.drain(); drainPipe(ab, qB, raB); qB.drain(); drainPipe(ba, qA, raA); qA.drain()
	}

	// 正式：2 秒
	var callCount atomic.Uint64
	var completed atomic.Int64
	responses.Store(0)

	start := time.Now()
	deadline := start.Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// 保持 inflight ≈ 500
		if callCount.Load()-uint64(completed.Load()) < 500 {
			callCount.Add(1)
			coreA.Call(corerpc.Target{ServerType: 2, Mode: corerpc.RoutingDirect,
				NodeID: lobbyID}, "Lobby/Test", []byte{0x08, 0x01},
				corerpc.Background(), func([]byte, errcode.ErrCode) {
					completed.Add(1)
				})
		}
		qA.drain()
		drainPipe(ab, qB, raB)
		qB.drain()
		drainPipe(ba, qA, raA)
		qA.drain()
	}

	elapsed := time.Since(start)
	calls := callCount.Load()
	cb := completed.Load()
	resps := responses.Load()

	t.Logf("")
	t.Logf("╔══════════════════════════════════════╗")
	t.Logf("║  真实框架模拟：异步 Call/OnResponse    ║")
	t.Logf("╠══════════════════════════════════════╣")
	t.Logf("║  耗时:     %v                       ║", elapsed.Round(time.Millisecond))
	t.Logf("║  发起:     %d Call                   ║", calls)
	t.Logf("║  Callback: %d                        ║", cb)
	t.Logf("║  响应率:   %.1f%%                     ║", float64(resps)/float64(calls)*100)
	t.Logf("║  吞吐:     %.0f RPC/s              ║", float64(cb)/elapsed.Seconds())
	t.Logf("╚══════════════════════════════════════╝")
}

type raTransport struct {
	ra  *routeragent.Module
	uds *routeragent.UDSConn
	q   *syncQueue
}

func (t *raTransport) SendFrame(target corerpc.Target, header corerpc.Header, body []byte) error {
	wireHead := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
		SeqID: header.SeqID, ServerType: header.ServerType,
		RoutingMode: uint8(header.RoutingMode), RoutingKey: header.RoutingKey,
		DeadlineMs: int64(target.Deadline / time.Millisecond),
		Route:      header.Route,
	})
	t.q.Post(func() {
		t.ra.RouteFrame(t.uds, routeragent.Frame{
			Type: routeragent.FrameRpcRequest, Header: wireHead, Body: body,
		})
	})
	return nil
}

func drainPipe(pipe chan []byte, q *syncQueue, ra *routeragent.Module) {
	for {
		select {
		case data := <-pipe:
			q.Post(func() { deliverToRA(ra, data) })
		default:
			return
		}
	}
}
