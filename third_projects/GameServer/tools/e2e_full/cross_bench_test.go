package main

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"project/internal/routeragent"
)

// TestCrossRATCPReal 跨双RA ping-pong 往返延迟 (单连接串行)
// RA-A 和 RA-B 各监听独立端口，通过真实TCP loopback连接
func TestCrossRATCPReal(t *testing.T) {
	qA, qB := &syncQueue{}, &syncQueue{}
	raA := routeragent.NewModuleForTest(qA.Post)
	raB := routeragent.NewModuleForTest(qB.Post)
	raA.AfterInit()
	raB.AfterInit()

	// ── 监听 ──
	lnA := listenTCP("127.0.0.1:0")
	lnB := listenTCP("127.0.0.1:0")
	defer lnA.Close()
	defer lnB.Close()
	addrA := lnA.Addr().String()
	addrB := lnB.Addr().String()
	t.Logf("RA-A: %s  RA-B: %s", addrA, addrB)

	raA.SetListenAddr(addrA)
	raA.PeerMgr().SetListenAddr(addrA)
	raB.SetListenAddr(addrB)
	raB.PeerMgr().SetListenAddr(addrB)

	// ── accept goroutines ──
	done := make(chan struct{})
	accept := func(ln net.Listener, ra *routeragent.Module, q *syncQueue, name string) {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				ln.(*net.TCPListener).SetDeadline(time.Now().Add(100 * time.Millisecond))
				conn, err := ln.Accept()
				if err != nil {
					continue
				}
				go handleIncoming(conn, ra, q)
			}
		}()
	}
	accept(lnA, raA, qA, "A")
	accept(lnB, raB, qB, "B")

	// ── UDS连接 ──
	gateUDS := routeragent.NewTestUDSConn(addrA)
	lobbyUDS := routeragent.NewTestUDSConn(addrB)
	gateID := uint32(0x00400001)
	lobbyID := uint32(0x00400002)

	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: addrA}, 1)
	raA.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: addrB}, 2)
	raA.RegisterConn(gateID, gateUDS)
	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: lobbyID, RAAddr: addrB}, 2)
	raB.MemberTable().Upsert(routeragent.NodeInfo{NodeID: gateID, RAAddr: addrA}, 1)
	raB.RegisterConn(lobbyID, lobbyUDS)

	// ── 建立双向 TCP ──
	if err := dialAndHandshake(raA, addrA, addrB, qA); err != nil {
		t.Fatalf("A→B dial: %v", err)
	}
	if err := dialAndHandshake(raB, addrB, addrA, qB); err != nil {
		t.Fatalf("B→A dial: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	qA.drain()
	qB.drain()

	// ── 稳定往返：连发1000次保证链路预热 ──
	ok := 0
	for i := 0; i < 1000; i++ {
		if oneRoundTrip(qA, qB, raA, raB, gateUDS, lobbyUDS, gateID, lobbyID, uint64(i)) {
			ok++
		}
	}
	t.Logf("预热: %d/1000", ok)

	// ── 正式统计 ──
	const N = 2000
	var success, latencySum atomic.Int64
	start := time.Now()
	for i := 0; i < N; i++ {
		t0 := time.Now()
		if oneRoundTrip(qA, qB, raA, raB, gateUDS, lobbyUDS, gateID, lobbyID, uint64(i+10000)) {
			success.Add(1)
			latencySum.Add(time.Since(t0).Microseconds())
		}
		drainUDS(gateUDS)
		drainUDS(lobbyUDS)
	}
	elapsed := time.Since(start)
	avg := time.Duration(0)
	if n := success.Load(); n > 0 {
		avg = time.Duration(latencySum.Load()/n) * time.Microsecond
	}

	t.Logf("")
	t.Logf("╔════════════════════════════════╗")
	t.Logf("║  跨双RA TCP Loopback 实测       ║")
	t.Logf("╠════════════════════════════════╣")
	t.Logf("║  成功率: %d/%d              ║", success.Load(), N)
	t.Logf("║  总耗时: %v           ║", elapsed.Round(time.Millisecond))
	t.Logf("║  均延:   %v/往返           ║", avg.Round(time.Nanosecond))
	t.Logf("║  吞吐:   %.0f RPC/s         ║", float64(success.Load())/elapsed.Seconds())
	t.Logf("╚════════════════════════════════╝")

	close(done)
	raA.BeforeStop()
	raB.BeforeStop()
}

func listenTCP(addr string) net.Listener {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		panic(err)
	}
	return ln
}

func handleIncoming(conn net.Conn, ra *routeragent.Module, q *syncQueue) {
	defer conn.Close()

	// 读对端握手: len(2B) + addr
	hsLen := mustReadUint16(conn)
	if hsLen > 1024 {
		return
	}
	hsAddr := make([]byte, hsLen)
	if _, err := io.ReadFull(conn, hsAddr); err != nil {
		return
	}
	peerAddr := string(hsAddr)

	// 回本端握手
	localAddr := ra.ListenAddr()
	reply := putUint16(uint16(len(localAddr)))
	reply = append(reply, localAddr...)
	conn.Write(reply)

	// 创建 peer link
	pl := newTCPLink(conn, peerAddr)
	go pl.writeLoop()
	ra.PeerMgr().Attach(peerAddr, pl)
	ra.PeerMgr().SetState(peerAddr, routeragent.PeerConnected)

	// 读循环
	for {
		data, err := readFrame(conn)
		if err != nil {
			return
		}
		q.Post(func() { deliverToRA(ra, data) })
		q.drain()
	}
}

func dialAndHandshake(m *routeragent.Module, local, remote string, q *syncQueue) error {
	conn, err := net.DialTimeout("tcp", remote, 3*time.Second)
	if err != nil {
		return err
	}

	// 发本端握手
	out := putUint16(uint16(len(local)))
	out = append(out, local...)
	conn.Write(out)

	// 读对端握手
	hsLen := mustReadUint16(conn)
	if hsLen > 1024 {
		conn.Close()
		return fmt.Errorf("bad hsLen %d", hsLen)
	}
	hs := make([]byte, hsLen)
	if _, err := io.ReadFull(conn, hs); err != nil {
		conn.Close()
		return err
	}
	peerAddr := string(hs)

	// 创建 peer link
	pl := newTCPLink(conn, peerAddr)
	go pl.writeLoop()

	// 读循环
	go func() {
		for {
			data, err := readFrame(conn)
			if err != nil {
				pl.close()
				return
			}
			q.Post(func() { deliverToRA(m, data) })
			q.drain()
		}
	}()

	m.PeerMgr().Attach(peerAddr, pl)
	m.PeerMgr().SetState(peerAddr, routeragent.PeerConnected)
	return nil
}

func oneRoundTrip(qA, qB *syncQueue, raA, raB *routeragent.Module,
	gateUDS, lobbyUDS *routeragent.UDSConn,
	gateID, lobbyID uint32, seq uint64) bool {

	reqHead := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
		SeqID: seq, ServerType: 2, RoutingMode: uint8(routeragent.RoutingModeDirect),
		RoutingKey: fmt.Sprintf("%d", lobbyID), Route: "Test/Cross",
	})

	// 1. gate → RA-A
	qA.Post(func() {
		raA.RouteFrame(gateUDS, routeragent.Frame{
			Type: routeragent.FrameRpcRequest, Header: reqHead, Body: []byte{0x08, 0x01},
		})
	})

	// 驱动直到 lobby 收到（TCP I/O 需要微秒级等待）
	for i := 0; i < 100; i++ {
		qA.drain()
		qB.drain()
		time.Sleep(time.Microsecond) // 让 TCP goroutine 有时间执行 I/O

		select {
		case lf := <-lobbyUDS.Recv():
			lh, _ := routeragent.DecodeRPCWireHeader(lf.Header)
			respH := routeragent.EncodeRPCWireHeader(routeragent.RPCWireHeader{
				SeqID: lh.SeqID, FromNodeID: gateID,
			})
			qB.Post(func() {
				raB.RouteFrame(lobbyUDS, routeragent.Frame{
					Type: routeragent.FrameRpcResponse, Header: respH, Body: []byte{0x10, 0x00},
				})
			})
			// 回包方向同样需要等待 TCP I/O
			for j := 0; j < 100; j++ {
				qB.drain()
				qA.drain()
				time.Sleep(time.Microsecond)
				select {
				case <-gateUDS.Recv():
					return true
				default:
				}
			}
			return false
		default:
		}
	}
	return false
}

func putUint16(v uint16) []byte {
	b := make([]byte, 2)
	b[0] = byte(v >> 8)
	b[1] = byte(v)
	return b
}

func mustReadUint16(r io.Reader) uint16 {
	b := make([]byte, 2)
	if _, err := io.ReadFull(r, b); err != nil {
		return 0
	}
	return uint16(b[0])<<8 | uint16(b[1])
}

func readFrame(r io.Reader) ([]byte, error) {
	lb := make([]byte, 4)
	if _, err := io.ReadFull(r, lb); err != nil {
		return nil, err
	}
	length := int(lb[0])<<24 | int(lb[1])<<16 | int(lb[2])<<8 | int(lb[3])
	if length < 3 || length > 16*1024*1024 {
		return nil, fmt.Errorf("bad length %d", length)
	}
	fb := make([]byte, length)
	if _, err := io.ReadFull(r, fb); err != nil {
		return nil, err
	}
	out := make([]byte, 4+length)
	copy(out[:4], lb)
	copy(out[4:], fb)
	return out, nil
}

func drainUDS(u *routeragent.UDSConn) {
	for {
		select {
		case <-u.Recv():
		default:
			return
		}
	}
}

// ── 简化的 tcpPeerLink ──

type tcpPeerLink2 struct {
	conn   net.Conn
	addr   string
	sendCh chan routeragent.Frame
	done   chan struct{}
	once   sync.Once
}

func newTCPLink(conn net.Conn, addr string) *tcpPeerLink2 {
	return &tcpPeerLink2{conn: conn, addr: addr, sendCh: make(chan routeragent.Frame, 64), done: make(chan struct{})}
}
func (l *tcpPeerLink2) Send(f routeragent.Frame) error {
	select {
	case <-l.done:
		return fmt.Errorf("closed")
	case l.sendCh <- f:
		return nil
	}
}
func (l *tcpPeerLink2) Close() error          { return l.conn.Close() }
func (l *tcpPeerLink2) close()                 { l.once.Do(func() { close(l.done); l.conn.Close() }) }
func (l *tcpPeerLink2) writeLoop() {
	for {
		select {
		case <-l.done:
			return
		case f := <-l.sendCh:
			data, _ := routeragent.EncodeFrame(f)
			l.conn.Write(data)
		}
	}
}
