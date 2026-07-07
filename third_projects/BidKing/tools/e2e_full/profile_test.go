package main

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"project/internal/routeragent"
)

func TestProfileLatency(t *testing.T) {
	qA, qB := &syncQueue{}, &syncQueue{}
	raA, raB := newRAsync(qA, "a"), newRAsync(qB, "b")
	defer raA.BeforeStop()
	defer raB.BeforeStop()

	g := routeragent.NewTestUDSConn("a")
	l := routeragent.NewTestUDSConn("b")
	gateID, lobbyID := uint32(0x00400001), uint32(0x00400002)

	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: "a"}, 1)
	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: "b"}, 2)
	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: "b"}, 2)
	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: "a"}, 1)
	raA.RegisterConn(gateID, g)
	raB.RegisterConn(lobbyID, l)

	ab := make(chan []byte, 512)
	ba := make(chan []byte, 512)
	raA.PeerMgr().Attach("b", routeragent.NewTCPPeerLink(&chWriter{ch: ab}, "b"))
	raB.PeerMgr().Attach("a", routeragent.NewTCPPeerLink(&chWriter{ch: ba}, "a"))
	raA.PeerMgr().SetState("b", routeragent.PeerConnected)
	raB.PeerMgr().SetState("a", routeragent.PeerConnected)

	// lobby 自动回包
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case lf := <-l.Recv():
				lh, _ := routeragent.DecodeRPCWireHeader(lf.Header)
				qB.Post(func() { raB.RouteFrame(l, routeragent.Frame{
					Type: routeragent.FrameRpcResponse,
					Header: routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{SeqID: lh.SeqID, FromNodeID: gateID}),
					Body:  []byte{0x10, 0x00},
				}) })
			case <-done: return
			}
		}
	}()

	// 预热
	for i := 0; i < 5000; i++ {
		qA.Post(func() { raA.RouteFrame(g, routeragent.Frame{Type: routeragent.FrameRpcRequest,
			Header: routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
				SeqID: uint64(i+1), ServerType: 2, RoutingMode: uint8(routeragent.RoutingModeDirect),
				RoutingKey: fmt.Sprintf("%d", lobbyID), Route: "Bench"}),
			Body: []byte{0x08, 0x01}}) })
		qA.drain(); pipeOnce(ab, qB, raB); qB.drain(); pipeOnce(ba, qA, raA); qA.drain()
	}
	
	// 计次统计各阶段耗时
	var tPost, tDrainA, tPipeAB, tDrainB, tPipeBA, tDrainA2 atomic.Int64
	var tEncode, tDispatch, tRoute, tDeliver, tAutoReply atomic.Int64
	var count atomic.Int64
	
	stop := make(chan struct{})
	go func() {
		seq := uint64(10000)
		for {
			select {
			case <-stop: return
			default:
			}
			seq++
			sid := seq
			t0 := time.Now()
			qA.Post(func() { raA.RouteFrame(g, routeragent.Frame{Type: routeragent.FrameRpcRequest,
				Header: routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
					SeqID: sid, ServerType: 2, RoutingMode: uint8(routeragent.RoutingModeDirect),
					RoutingKey: fmt.Sprintf("%d", lobbyID), Route: "Bench"}),
				Body: []byte{0x08, 0x01}}) })
			tPost.Add(time.Since(t0).Nanoseconds())
		}
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		t0 := time.Now()
		qA.drain()
		tDrainA.Add(time.Since(t0).Nanoseconds())
		
		t0 = time.Now()
		pipeOnce(ab, qB, raB)
		tPipeAB.Add(time.Since(t0).Nanoseconds())
		
		t0 = time.Now()
		qB.drain()
		tDrainB.Add(time.Since(t0).Nanoseconds())
		
		t0 = time.Now()
		pipeOnce(ba, qA, raA)
		tPipeBA.Add(time.Since(t0).Nanoseconds())
		
		t0 = time.Now()
		qA.drain()
		tDrainA2.Add(time.Since(t0).Nanoseconds())
		
		count.Add(1)
	}
	close(stop)
	
	n := count.Load()
	t.Logf("循环次数: %d", n)
	t.Logf("平均耗时/轮 (ns):")
	t.Logf("  Post入队:  %d", tPost.Load()/n)
	t.Logf("  qA.drain:  %d", tDrainA.Load()/n)
	t.Logf("  pipeA→B:   %d", tPipeAB.Load()/n)
	t.Logf("  qB.drain:  %d", tDrainB.Load()/n)
	t.Logf("  pipeB→A:   %d", tPipeBA.Load()/n)
	t.Logf("  qA.drain2: %d", tDrainA2.Load()/n)
	t.Logf("  总/轮:     %d ns (%.0f rps)", (tPost.Load()+tDrainA.Load()+tPipeAB.Load()+tDrainB.Load()+tPipeBA.Load()+tDrainA2.Load())/n,
		float64(n)/time.Second.Seconds())
	_ = tEncode; _ = tDispatch; _ = tRoute; _ = tDeliver; _ = tAutoReply
}

func pipeOnce(pipe chan []byte, q *syncQueue, ra *routeragent.Module) {
	select {
	case data := <-pipe:
		q.Post(func() { deliverToRA(ra, data) })
	default:
	}
}
