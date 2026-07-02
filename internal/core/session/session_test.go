package session

import (
	"testing"

	"project/internal/core/codec"
	"project/internal/core/conn"
)

type stubConn struct {
	addr string
}

func (c *stubConn) Send([]byte)                {}
func (c *stubConn) Close() error               { return nil }
func (c *stubConn) RemoteAddr() string         { return c.addr }
func (c *stubConn) Done() <-chan struct{}      { return nil }
func (c *stubConn) LastRecvUnixNano() int64    { return 0 }
func (c *stubConn) TouchRecv()                 {}
func (c *stubConn) Recv() <-chan *codec.Packet { return nil }

var _ conn.Connection = (*stubConn)(nil)

func TestNewSessionManager(t *testing.T) {
	m := NewSessionManager()
	if m == nil {
		t.Fatal("NewSessionManager returned nil")
	}
}

func TestOnConnect(t *testing.T) {
	m := NewSessionManager()
	c := &stubConn{addr: "192.168.1.1:8000"}
	sess := m.OnConnect(c)
	if sess == nil {
		t.Fatal("OnConnect returned nil")
	}
	if sess.ConnID != "192.168.1.1:8000" {
		t.Errorf("ConnID=%q, want 192.168.1.1:8000", sess.ConnID)
	}
	if sess.Authed {
		t.Error("new session should not be authed")
	}
}

func TestOnDisconnect(t *testing.T) {
	m := NewSessionManager()
	c := &stubConn{addr: "10.0.0.1:9000"}
	sess := m.OnConnect(c)
	sess.UID = 100
	sess.Authed = true
	m.byUID[100] = sess

	m.OnDisconnect(c)
	if s := m.GetByConnID("10.0.0.1:9000"); s != nil {
		t.Error("session should be removed after disconnect")
	}
	if s := m.GetByUID(100); s != nil {
		t.Error("UID index should be cleaned after disconnect")
	}
}

func TestBindSession(t *testing.T) {
	m := NewSessionManager()
	c := &stubConn{addr: "10.0.0.2:9000"}
	_ = m.OnConnect(c)
	bound := map[uint32]uint32{2: 42}
	s := m.BindSession(c.RemoteAddr(), 200, bound)
	if s == nil {
		t.Fatal("BindSession returned nil")
	}
	if s.UID != 200 {
		t.Errorf("UID=%d, want 200", s.UID)
	}
	if !s.Authed {
		t.Error("BindSession should set Authed=true")
	}
	if s.BoundNodes[2] != 42 {
		t.Errorf("BoundNodes[2]=%d, want 42", s.BoundNodes[2])
	}
}

func TestGetByUID(t *testing.T) {
	m := NewSessionManager()
	c := &stubConn{addr: "10.0.0.3:9000"}
	m.BindSession(c.RemoteAddr(), 300, nil)

	if s := m.GetByUID(300); s == nil {
		t.Error("GetByUID returned nil")
	}
	if s := m.GetByUID(999); s != nil {
		t.Error("GetByUID should return nil for unknown UID")
	}
}

func TestSetBound(t *testing.T) {
	m := NewSessionManager()
	c := &stubConn{addr: "10.0.0.4:9000"}
	m.BindSession(c.RemoteAddr(), 400, map[uint32]uint32{2: 10})

	m.SetBound(400, 3, 20)
	s := m.GetByUID(400)
	if s == nil {
		t.Fatal("session not found")
	}
	if s.BoundNodes[3] != 20 {
		t.Errorf("BoundNodes[3]=%d, want 20", s.BoundNodes[3])
	}
	if s.BoundNodes[2] != 10 {
		t.Errorf("original BoundNodes[2] should be preserved: got %d, want 10", s.BoundNodes[2])
	}
}

func TestOnTimeout(t *testing.T) {
	m := NewSessionManager()
	c := &stubConn{addr: "10.0.0.5:9000"}
	m.OnConnect(c)
	m.OnTimeout(c)
	if s := m.GetByConnID("10.0.0.5:9000"); s != nil {
		t.Error("session should be removed after timeout")
	}
}

func TestSessionSetBound(t *testing.T) {
	s := &Session{ID: "test"}
	s.SetBound(2, 100)
	if s.BoundNodes[2] != 100 {
		t.Errorf("BoundNodes[2]=%d, want 100", s.BoundNodes[2])
	}
	s.SetAuthed(true)
	if !s.Authed {
		t.Error("SetAuthed should set to true")
	}
}
