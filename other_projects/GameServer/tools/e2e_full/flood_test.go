package main

import (
	"fmt"
	"testing"
	"time"

	"project/internal/routeragent"
)

// TestCrossRASingleThread 单线程 drain + channel pipe 模拟真实场景
// 真实架构：一个主循环 goroutine 同时 drain qA 和 qB
// TCP 用 in-memory channel 模拟（零拷贝，纯 CPU）
func TestCrossRASingleThread(t *testing.T) {
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
	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: "a"}, 1) // 回包路由需要
	raA.RegisterConn(gateID, g)
	raB.RegisterConn(lobbyID, l)

	// TCP pipe 用 channel 模拟
	ab := make(chan []byte, 64)
	ba := make(chan []byte, 64)
	raA.PeerMgr().Attach("b", routeragent.NewTCPPeerLink(&chWriter{ch: ab}, "b"))
	raB.PeerMgr().Attach("a", routeragent.NewTCPPeerLink(&chWriter{ch: ba}, "a"))
	raA.PeerMgr().SetState("b", routeragent.PeerConnected)
	raB.PeerMgr().SetState("a", routeragent.PeerConnected)

	// lobby 自动回包
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case lf := <-l.Recv():
				lh, _ := routeragent.DecodeRPCWireHeader(lf.Header)
				qB.Post(func() {
					raB.RouteFrame(l, routeragent.Frame{
						Type:   routeragent.FrameRpcResponse,
						Header: routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
							SeqID: lh.SeqID, FromNodeID: gateID,
						}),
						Body: []byte{0x10, 0x00},
					})
				})
			case <-stop:
				return
			}
		}
	}()

	// ============ 单线程 drain：模拟真实主循环 ============
	// 同时 drain qA + qB + TCP channel pipe + 计数 gate 回收
	var seq uint64
	var sent, recv int
	drainStart := time.Now()
	deadline := drainStart.Add(2 * time.Second)

	for time.Now().Before(deadline) {
		// 1. 发新请求（控制并发度：一次最多 inflight N 个）
		if seq < 200000 && sent-recv < 500 {
			seq++
			sent++
			sid := seq
			qA.Post(func() {
				raA.RouteFrame(g, routeragent.Frame{
					Type:   routeragent.FrameRpcRequest,
					Header: routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
						SeqID: sid, ServerType: 2,
						RoutingMode: uint8(routeragent.RoutingModeDirect),
						RoutingKey: fmt.Sprintf("%d", lobbyID),
						Route:      "Bench",
					}),
					Body: []byte{0x08, 0x01},
				})
			})
		}

		// 2. Drain RA-A 主循环
		qA.drain()

		// 3. 拉取 RA-A→RA-B 的 TCP 管道帧
		select {
		case data := <-ab:
			qB.Post(func() { deliverToRA(raB, data) })
		default:
		}

		// 4. Drain RA-B 主循环
		qB.drain()

		// 5. 拉取 RA-B→RA-A 的 TCP 管道帧
		select {
		case data := <-ba:
			qA.Post(func() { deliverToRA(raA, data) })
		default:
		}
		qA.drain()

		// 6. 收集 gate 收到的响应
		select {
		case <-g.Recv():
			recv++
		default:
		}
	}

	elapsed := time.Since(drainStart)
	// 清空残留
	for i := 0; i < 1000; i++ {
		qA.drain(); qB.drain()
		select { case <-g.Recv(): recv++; default: }
		select { case <-l.Recv(): default: }
	}

	t.Logf("")
	t.Logf("╔══════════════════════════════════════╗")
	t.Logf("║  单线程 drain — 真实主循环吞吐         ║")
	t.Logf("╠══════════════════════════════════════╣")
	t.Logf("║  耗时:     %v                        ║", elapsed.Round(time.Millisecond))
	t.Logf("║  发送:     %d 请求                    ║", sent)
	t.Logf("║  回收:     %d 响应                    ║", recv)
	t.Logf("║  吞吐:     %.0f RPC/s               ║", float64(recv)/elapsed.Seconds())
	t.Logf("║  均延:     %.0f ns/往返              ║", float64(elapsed.Nanoseconds())/float64(recv))
	t.Logf("║  inflight: 500                        ║")
	t.Logf("╚══════════════════════════════════════╝")
}

func newRAsync(q *syncQueue, addr string) *routeragent.Module {
	m := routeragent.NewModuleForTest(q.Post)
	m.AfterInit()
	m.SetListenAddr(addr)
	m.PeerMgr().SetListenAddr(addr)
	return m
}
