package acceptor

import (
	"testing"
	"time"
)

func TestWSAcceptorListenAndClose(t *testing.T) {
	a := NewWSAcceptor("127.0.0.1:0", "/ws")
	if err := a.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	// 获取实际监听地址
	time.Sleep(100 * time.Millisecond)

	if err := a.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestWSAcceptorInterface(t *testing.T) {
	var _ Acceptor = NewWSAcceptor(":0", "/ws")
}

func TestWSAcceptorNewPath(t *testing.T) {
	a := NewWSAcceptor(":0", "")
	if a.path != "/ws" {
		t.Errorf("default path: %q, want /ws", a.path)
	}
}

func TestWSConnectionWebSocket(t *testing.T) {
	a := NewWSAcceptor("127.0.0.1:0", "/ws")
	if err := a.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer a.Close()

	time.Sleep(100 * time.Millisecond)

	// 验证 wsConnection RemoteAddr 在 remoteAddr 已设置时不 panic
	ws := wsConnection{remoteAddr: "test:1", done: make(chan struct{})}
	close(ws.done)
	addr := ws.RemoteAddr()
	if addr != "test:1" {
		t.Errorf("RemoteAddr=%q, want test:1", addr)
	}
}

func TestConnID(t *testing.T) {
	id1 := connID()
	id2 := connID()
	if id1 == id2 {
		t.Error("connID should generate unique values")
	}
	if len(id1) != 32 {
		t.Errorf("connID length=%d, want 32", len(id1))
	}
}

func TestWSAcceptorEmptyAddr(t *testing.T) {
	a := NewWSAcceptor("", "/ws")
	if err := a.Listen(); err == nil {
		t.Error("expected error for empty addr")
	}
}

func TestWSUpgrader(t *testing.T) {
	// 仅验证接口合规，不实际建立 WebSocket 连接
	a := NewWSAcceptor("127.0.0.1:0", "/ws")
	if err := a.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer a.Close()
	time.Sleep(50 * time.Millisecond)
	// srv.Addr 在 Listen 后应该是 "127.0.0.1:<port>"
	if a.srv.Addr == "" {
		t.Error("srv.Addr should be set after Listen")
	}
}
