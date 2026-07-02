package dispatcher

import (
	"fmt"
	"testing"

	"project/internal/core/codec"
	"project/internal/core/errcode"
	"project/internal/core/session"
)

func TestDispatchMissingRoute(t *testing.T) {
	d := New(1)
	err := d.Dispatch(session.NewSessionManager().BindSession("c", 1, nil), &codec.Message{CmdID: 1})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRecoverMiddleware(t *testing.T) {
	d := New(1)
	d.Use(RecoverMiddleware())
	d.RegisterHandler(100, func(*session.Session, *codec.Message) error {
		panic("boom")
	})
	sess := session.NewSessionManager().BindSession("c", 1, nil)
	err := d.Dispatch(sess, &codec.Message{CmdID: 100})
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
	if errcode.CodeOf(err) != errcode.ERR_INTERNAL {
		t.Errorf("expected ERR_INTERNAL (1), got %v", errcode.CodeOf(err))
	}
}

func TestRecoverMiddlewareNoPanic(t *testing.T) {
	d := New(1)
	d.Use(RecoverMiddleware())
	d.RegisterHandler(100, func(*session.Session, *codec.Message) error {
		return nil
	})
	sess := session.NewSessionManager().BindSession("c", 1, nil)
	if err := d.Dispatch(sess, &codec.Message{CmdID: 100}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAuthMiddleware_Whitelisted(t *testing.T) {
	d := New(1)
	d.Use(AuthMiddleware(map[uint32]bool{100: true}))
	var called bool
	d.RegisterHandler(100, func(*session.Session, *codec.Message) error {
		called = true
		return nil
	})
	// 未认证但 cmdID 在白名单中
	sess := session.NewSessionManager().BindSession("c", 0, nil)
	sess.Authed = false
	if err := d.Dispatch(sess, &codec.Message{CmdID: 100}); err != nil {
		t.Errorf("unexpected auth error for whitelisted cmd: %v", err)
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestAuthMiddleware_NotAuthed(t *testing.T) {
	d := New(1)
	d.Use(AuthMiddleware(map[uint32]bool{}))
	d.RegisterHandler(200, func(*session.Session, *codec.Message) error {
		return nil
	})
	sess := session.NewSessionManager().BindSession("c", 0, nil)
	sess.Authed = false
	err := d.Dispatch(sess, &codec.Message{CmdID: 200})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if errcode.CodeOf(err) != errcode.ERR_UNAUTHED {
		t.Errorf("expected ERR_UNAUTHED, got %v", errcode.CodeOf(err))
	}
}

func TestAuthMiddleware_Authed(t *testing.T) {
	d := New(1)
	d.Use(AuthMiddleware(map[uint32]bool{}))
	var called bool
	d.RegisterHandler(200, func(*session.Session, *codec.Message) error {
		called = true
		return nil
	})
	sess := session.NewSessionManager().BindSession("c", 1, nil)
	sess.Authed = true
	if err := d.Dispatch(sess, &codec.Message{CmdID: 200}); err != nil {
		t.Errorf("unexpected error for authed session: %v", err)
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestMultipleMiddlewares(t *testing.T) {
	d := New(1)
	d.Use(RecoverMiddleware())
	d.Use(AuthMiddleware(map[uint32]bool{}))
	var called bool
	d.RegisterHandler(300, func(*session.Session, *codec.Message) error {
		called = true
		return nil
	})
	// 已认证
	sess := session.NewSessionManager().BindSession("c", 1, nil)
	sess.Authed = true
	if err := d.Dispatch(sess, &codec.Message{CmdID: 300}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler was not called")
	}
	// 未认证
	sess2 := session.NewSessionManager().BindSession("c2", 0, nil)
	sess2.Authed = false
	err := d.Dispatch(sess2, &codec.Message{CmdID: 300})
	if errcode.CodeOf(err) != errcode.ERR_UNAUTHED {
		t.Errorf("expected ERR_UNAUTHED, got %v", err)
	}
}

func TestMiddlewareChainError(t *testing.T) {
	d := New(1)
	d.Use(func(next HandlerFunc) HandlerFunc {
		return func(sess *session.Session, msg *codec.Message) error {
			return fmt.Errorf("middleware blocked")
		}
	})
	var called bool
	d.RegisterHandler(400, func(*session.Session, *codec.Message) error {
		called = true
		return nil
	})
	sess := session.NewSessionManager().BindSession("c", 1, nil)
	err := d.Dispatch(sess, &codec.Message{CmdID: 400})
	if err == nil {
		t.Fatal("expected middleware error")
	}
	if called {
		t.Error("handler should not be called after middleware error")
	}
}

func TestDispatchForwardToBackend(t *testing.T) {
	d := New(1)
	d.RegisterRoute(500, RouteEntry{CmdID: 500, ServerType: 2, Route: "Test/Foo", RspCmdID: 501})
	var forwarded bool
	d.SetForward(func(sess *session.Session, msg *codec.Message, entry RouteEntry) error {
		forwarded = true
		if entry.ServerType != 2 {
			t.Errorf("expected ServerType 2, got %d", entry.ServerType)
		}
		return nil
	})
	sess := session.NewSessionManager().BindSession("c", 1, nil)
	if err := d.Dispatch(sess, &codec.Message{CmdID: 500}); err != nil {
		t.Errorf("unexpected dispatch error: %v", err)
	}
	if !forwarded {
		t.Error("forward was not called")
	}
}

func TestDispatchNilMessage(t *testing.T) {
	d := New(1)
	err := d.Dispatch(session.NewSessionManager().BindSession("c", 1, nil), nil)
	if errcode.CodeOf(err) != errcode.ERR_UNMARSHAL {
		t.Errorf("expected ERR_UNMARSHAL for nil message, got %v", err)
	}
}

func TestGateDispatcherHeartbeat(t *testing.T) {
	g := NewGateDispatcher(1, session.NewSessionManager())
	tc := newTestConn("test:1")
	if err := g.HandlePacket(tc, &codec.Packet{Type: codec.PacketHeartbeat}); err != nil {
		t.Errorf("heartbeat should not error: %v", err)
	}
}

func TestGateDispatcherNilPacket(t *testing.T) {
	g := NewGateDispatcher(1, session.NewSessionManager())
	err := g.HandlePacket(nil, nil)
	if errcode.CodeOf(err) != errcode.ERR_UNMARSHAL {
		t.Errorf("expected ERR_UNMARSHAL for nil packet, got %v", err)
	}
}

// testConn is a minimal Connection for testing
type testConn struct {
	addr string
}

func newTestConn(addr string) *testConn        { return &testConn{addr: addr} }
func (c *testConn) Send([]byte)                {}
func (c *testConn) Close() error               { return nil }
func (c *testConn) RemoteAddr() string         { return c.addr }
func (c *testConn) Done() <-chan struct{}      { return nil }
func (c *testConn) LastRecvUnixNano() int64    { return 0 }
func (c *testConn) TouchRecv()                 {}
func (c *testConn) Recv() <-chan *codec.Packet { return nil }
