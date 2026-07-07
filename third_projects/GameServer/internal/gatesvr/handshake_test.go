package gatesvr

import (
	"encoding/binary"
	"testing"

	"project/internal/core/codec"
	"project/internal/core/conn"
	"project/internal/core/errcode"
)

func TestDecodeHandshake(t *testing.T) {
	buf := make([]byte, 3)
	binary.BigEndian.PutUint16(buf[0:2], 1) // version
	buf[2] = 2                              // platform

	hs, err := decodeHandshake(buf)
	if err != nil {
		t.Fatalf("decodeHandshake: %v", err)
	}
	if hs.Version != 1 {
		t.Errorf("version=%d, want 1", hs.Version)
	}
	if hs.Platform != 2 {
		t.Errorf("platform=%d, want 2", hs.Platform)
	}
}

func TestDecodeHandshakeTooShort(t *testing.T) {
	_, err := decodeHandshake([]byte{0, 0})
	if err == nil {
		t.Error("expected error for short body")
	}
}

func TestEncodeHandshakeAck(t *testing.T) {
	body := HandshakeAckBody{
		ErrCode:           0,
		HeartbeatInterval: 10,
		ServerTime:        1234567890,
	}
	data := encodeHandshakeAck(body)
	if len(data) != 4+2+8 {
		t.Errorf("length=%d, want %d", len(data), 4+2+8)
	}
	errCode := binary.BigEndian.Uint32(data[0:4])
	if errCode != 0 {
		t.Errorf("errCode=%d, want 0", errCode)
	}
	interval := binary.BigEndian.Uint16(data[4:6])
	if interval != 10 {
		t.Errorf("interval=%d, want 10", interval)
	}
}

func TestHandshakeVersionMismatch(t *testing.T) {
	m := &Module{}
	m.sessions = nil // not initialized, but handleHandshake uses sessions.OnConnect

	buf := make([]byte, 3)
	binary.BigEndian.PutUint16(buf[0:2], 999) // wrong version
	buf[2] = 1

	// 验证不会 panic，只是返回 false
	// (sessions=nil 会导致 OnConnect 返回 nil，最终返回 false)
	testConn := newTestConn2("test:1")
	result := m.handleHandshake(testConn, buf)
	if result {
		t.Error("expected false for version mismatch")
	}
}

// testConn2 用于握手测试
type testConn2 struct {
	addr   string
	sent   [][]byte
	closed bool
}

func newTestConn2(addr string) *testConn2       { return &testConn2{addr: addr} }
func (c *testConn2) Send(data []byte)           { c.sent = append(c.sent, data) }
func (c *testConn2) Close() error               { c.closed = true; return nil }
func (c *testConn2) RemoteAddr() string         { return c.addr }
func (c *testConn2) Done() <-chan struct{}      { return nil }
func (c *testConn2) LastRecvUnixNano() int64    { return 0 }
func (c *testConn2) TouchRecv()                 {}
func (c *testConn2) Recv() <-chan *codec.Packet { return nil }

var _ conn.Connection = (*testConn2)(nil)

func TestSendHandshakeAckOK(t *testing.T) {
	c := newTestConn2("tcp:test:1")

	buf := make([]byte, 3)
	binary.BigEndian.PutUint16(buf[0:2], HandshakeVersion)
	buf[2] = 1

	mod := NewModule(":0")
	mod.sessions = nil // 未初始化，但 sendHandshakeAck 直接调用 conn.Send
	mod.sendHandshakeAck(c, errcode.OK, 10)

	if len(c.sent) != 1 {
		t.Fatalf("expected 1 sent packet, got %d", len(c.sent))
	}
	// 解析返回的 packet
	pkt, err := codec.DecodePacket(c.sent[0])
	if err != nil {
		t.Fatalf("DecodePacket: %v", err)
	}
	if pkt.Type != codec.PacketHandshakeAck {
		t.Errorf("type=%d, want PacketHandshakeAck", pkt.Type)
	}
	if c.closed {
		t.Error("connection should not be closed on OK")
	}
}

func TestSendHandshakeAckError(t *testing.T) {
	c := newTestConn2("tcp:test:2")
	mod := NewModule(":0")
	mod.sendHandshakeAck(c, errcode.ERR_UNAUTHED, 0)

	if !c.closed {
		t.Error("connection should be closed on error")
	}
}
