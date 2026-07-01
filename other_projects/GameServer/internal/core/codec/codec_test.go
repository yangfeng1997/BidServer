package codec

import (
	"encoding/binary"
	"testing"

	"project/internal/core/errcode"
)

// ---- message 编解码 ----

func TestEncodeDecodeRequest(t *testing.T) {
	orig := Message{
		Type:  MessageRequest,
		SeqID: 42,
		CmdID: 2050,
		Body:  []byte("hello"),
	}
	data, err := EncodeMessage(orig)
	if err != nil {
		t.Fatalf("EncodeMessage error: %v", err)
	}
	decoded, err := DecodeMessage(data)
	if err != nil {
		t.Fatalf("DecodeMessage error: %v", err)
	}
	if decoded.Type != MessageRequest {
		t.Errorf("type=%d, want %d", decoded.Type, MessageRequest)
	}
	if decoded.SeqID != 42 {
		t.Errorf("seqID=%d, want 42", decoded.SeqID)
	}
	if decoded.CmdID != 2050 {
		t.Errorf("cmdID=%d, want 2050", decoded.CmdID)
	}
	if string(decoded.Body) != "hello" {
		t.Errorf("body=%q, want hello", decoded.Body)
	}
}

func TestEncodeDecodeResponse(t *testing.T) {
	orig := Message{
		Type:    MessageResponse,
		SeqID:   100,
		CmdID:   2051,
		ErrCode: errcode.OK,
		Body:    []byte("world"),
	}
	data, err := EncodeMessage(orig)
	if err != nil {
		t.Fatalf("EncodeMessage error: %v", err)
	}
	decoded, err := DecodeMessage(data)
	if err != nil {
		t.Fatalf("DecodeMessage error: %v", err)
	}
	if decoded.ErrCode != errcode.OK {
		t.Errorf("errCode=%d, want OK", decoded.ErrCode)
	}
	if string(decoded.Body) != "world" {
		t.Errorf("body=%q, want world", decoded.Body)
	}
}

func TestEncodeDecodeErrorResponse(t *testing.T) {
	orig := Message{
		Type:    MessageResponse,
		SeqID:   1,
		CmdID:   2051,
		ErrCode: errcode.ERR_TIMEOUT,
	}
	data, err := EncodeMessage(orig)
	if err != nil {
		t.Fatalf("EncodeMessage error: %v", err)
	}
	decoded, err := DecodeMessage(data)
	if err != nil {
		t.Fatalf("DecodeMessage error: %v", err)
	}
	if decoded.ErrCode != errcode.ERR_TIMEOUT {
		t.Errorf("errCode=%d, want ERR_TIMEOUT", decoded.ErrCode)
	}
}

func TestEncodeDecodeNotify(t *testing.T) {
	orig := Message{
		Type:  MessageNotify,
		CmdID: 2052,
		Body:  []byte("ping"),
	}
	data, err := EncodeMessage(orig)
	if err != nil {
		t.Fatalf("EncodeMessage error: %v", err)
	}
	// Notify 无 seqID
	if len(data) != 1+4+4 {
		t.Errorf("notify length=%d, want %d", len(data), 1+4+4)
	}
	decoded, err := DecodeMessage(data)
	if err != nil {
		t.Fatalf("DecodeMessage error: %v", err)
	}
	if decoded.Type != MessageNotify {
		t.Errorf("type=%d, want Notify", decoded.Type)
	}
	if decoded.CmdID != 2052 {
		t.Errorf("cmdID=%d, want 2052", decoded.CmdID)
	}
}

func TestDecodeMessageEmpty(t *testing.T) {
	_, err := DecodeMessage([]byte{})
	if err == nil {
		t.Error("expected error for empty data")
	}
}

func TestDecodeMessageUnknownType(t *testing.T) {
	_, err := DecodeMessage([]byte{0xFF, 0, 0, 0, 0, 0, 0})
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestDecodeMessageShortRequest(t *testing.T) {
	_, err := DecodeMessage([]byte{byte(MessageRequest), 0, 0, 0, 0, 0}) // only 6 bytes, need 7
	if err == nil {
		t.Error("expected error for short request")
	}
}

func TestDecodeMessageShortResponse(t *testing.T) {
	_, err := DecodeMessage([]byte{byte(MessageResponse), 0, 0, 0, 0, 0, 0, 0, 0, 0}) // 10 bytes, need 11
	if err == nil {
		t.Error("expected error for short response")
	}
}

func TestDecodeMessageShortNotify(t *testing.T) {
	_, err := DecodeMessage([]byte{byte(MessageNotify), 0, 0, 0}) // 4 bytes, need 5
	if err == nil {
		t.Error("expected error for short notify")
	}
}

func TestEncodeMessageUnknownType(t *testing.T) {
	_, err := EncodeMessage(Message{Type: 0xFF})
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestMessageRoundTripLargeBody(t *testing.T) {
	body := make([]byte, 65536)
	for i := range body {
		body[i] = byte(i % 256)
	}
	orig := Message{Type: MessageRequest, SeqID: 1, CmdID: 100, Body: body}
	data, err := EncodeMessage(orig)
	if err != nil {
		t.Fatalf("EncodeMessage error: %v", err)
	}
	decoded, err := DecodeMessage(data)
	if err != nil {
		t.Fatalf("DecodeMessage error: %v", err)
	}
	if string(decoded.Body) != string(body) {
		t.Error("large body round trip mismatch")
	}
}

// ---- packet 编解码 ----

func TestEncodeDecodePacket(t *testing.T) {
	orig := Packet{Type: PacketData, Body: []byte("data payload")}
	data, err := EncodePacket(orig)
	if err != nil {
		t.Fatalf("EncodePacket error: %v", err)
	}
	if len(data) != 4+len(orig.Body) {
		t.Errorf("length=%d, want %d", len(data), 4+len(orig.Body))
	}
	decoded, err := DecodePacket(data)
	if err != nil {
		t.Fatalf("DecodePacket error: %v", err)
	}
	if decoded.Type != PacketData {
		t.Errorf("type=%d, want PacketData", decoded.Type)
	}
	if string(decoded.Body) != "data payload" {
		t.Errorf("body=%q, want 'data payload'", decoded.Body)
	}
}

func TestEncodeDecodeHeartbeat(t *testing.T) {
	orig := Packet{Type: PacketHeartbeat}
	data, err := EncodePacket(orig)
	if err != nil {
		t.Fatalf("EncodePacket error: %v", err)
	}
	decoded, err := DecodePacket(data)
	if err != nil {
		t.Fatalf("DecodePacket error: %v", err)
	}
	if decoded.Type != PacketHeartbeat {
		t.Errorf("type=%d, want PacketHeartbeat", decoded.Type)
	}
	if len(decoded.Body) != 0 {
		t.Errorf("heartbeat body should be empty, got %d bytes", len(decoded.Body))
	}
}

func TestDecodePacketTooShort(t *testing.T) {
	_, err := DecodePacket([]byte{0, 0, 0})
	if err == nil {
		t.Error("expected error for packet too short")
	}
}

func TestDecodePacketLengthMismatch(t *testing.T) {
	data := make([]byte, 10)
	data[0] = byte(PacketData)
	putUint24(data[1:4], 100) // claim 100 bytes body, only 6 remain
	_, err := DecodePacket(data)
	if err == nil {
		t.Error("expected error for length mismatch")
	}
}

func TestEncodePacketTooLarge(t *testing.T) {
	p := Packet{Type: PacketData, Body: make([]byte, 0x1000000)} // exceeds 24-bit
	_, err := EncodePacket(p)
	if err == nil {
		t.Error("expected error for too large packet")
	}
}

func TestPacketAllTypes(t *testing.T) {
	types := []PacketType{PacketHandshake, PacketHandshakeAck, PacketHeartbeat, PacketData, PacketKick}
	for _, typ := range types {
		p := Packet{Type: typ}
		data, err := EncodePacket(p)
		if err != nil {
			t.Errorf("EncodePacket type=%d: %v", typ, err)
			continue
		}
		decoded, err := DecodePacket(data)
		if err != nil {
			t.Errorf("DecodePacket type=%d: %v", typ, err)
			continue
		}
		if decoded.Type != typ {
			t.Errorf("type round trip: got %d, want %d", decoded.Type, typ)
		}
	}
}

func TestUint24RoundTrip(t *testing.T) {
	buf := make([]byte, 3)
	for _, v := range []uint32{0, 1, 255, 65535, 0xABCDEF, 0xFFFFFF} {
		putUint24(buf, v)
		got := readUint24(buf)
		if got != v {
			t.Errorf("uint24 round trip: %d → %d", v, got)
		}
	}
}

func TestPutUint32(t *testing.T) {
	buf := make([]byte, 4)
	PutUint32(buf, 0xDEADBEEF)
	if Uint32(buf) != 0xDEADBEEF {
		t.Errorf("uint32 round trip failed: got %x", Uint32(buf))
	}
}

func TestMessageTypes(t *testing.T) {
	if MessageRequest != 0x01 {
		t.Errorf("MessageRequest=%d, want 0x01", MessageRequest)
	}
	if MessageResponse != 0x02 {
		t.Errorf("MessageResponse=%d, want 0x02", MessageResponse)
	}
	if MessageNotify != 0x03 {
		t.Errorf("MessageNotify=%d, want 0x03", MessageNotify)
	}
}

func TestPacketTypes(t *testing.T) {
	if PacketHandshake != 0x01 {
		t.Errorf("PacketHandshake=%d, want 0x01", PacketHandshake)
	}
	if PacketData != 0x04 {
		t.Errorf("PacketData=%d, want 0x04", PacketData)
	}
	if PacketKick != 0x05 {
		t.Errorf("PacketKick=%d, want 0x05", PacketKick)
	}
}

func TestEncodeRequestSize(t *testing.T) {
	m := Message{Type: MessageRequest, SeqID: 1, CmdID: 2050, Body: make([]byte, 100)}
	data, err := EncodeMessage(m)
	if err != nil {
		t.Fatalf("EncodeMessage: %v", err)
	}
	// Request = type(1) + seq(2) + cmd(4) + body(N)
	if len(data) != 1+2+4+100 {
		t.Errorf("request size=%d, want %d", len(data), 1+2+4+100)
	}
}

func TestEncodeResponseSize(t *testing.T) {
	m := Message{Type: MessageResponse, SeqID: 1, CmdID: 2051, ErrCode: errcode.OK, Body: make([]byte, 50)}
	data, err := EncodeMessage(m)
	if err != nil {
		t.Fatalf("EncodeMessage: %v", err)
	}
	// Response = type(1) + seq(2) + cmd(4) + err(4) + body(N)
	if len(data) != 1+2+4+4+50 {
		t.Errorf("response size=%d, want %d", len(data), 1+2+4+4+50)
	}
}

func TestEncodeNotifySize(t *testing.T) {
	m := Message{Type: MessageNotify, CmdID: 2052, Body: make([]byte, 200)}
	data, err := EncodeMessage(m)
	if err != nil {
		t.Fatalf("EncodeMessage: %v", err)
	}
	// Notify = type(1) + cmd(4) + body(N)
	if len(data) != 1+4+200 {
		t.Errorf("notify size=%d, want %d", len(data), 1+4+200)
	}
}

// Benchmark
func BenchmarkEncodeRequest(b *testing.B) {
	m := Message{Type: MessageRequest, SeqID: 1, CmdID: 2050, Body: make([]byte, 256)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeMessage(m)
	}
}

func BenchmarkDecodeRequest(b *testing.B) {
	m := Message{Type: MessageRequest, SeqID: 1, CmdID: 2050, Body: make([]byte, 256)}
	data, _ := EncodeMessage(m)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DecodeMessage(data)
	}
}

func BenchmarkEncodePacket(b *testing.B) {
	p := Packet{Type: PacketData, Body: make([]byte, 256)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodePacket(p)
	}
}

func BenchmarkDecodePacket(b *testing.B) {
	p := Packet{Type: PacketData, Body: make([]byte, 256)}
	data, _ := EncodePacket(p)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DecodePacket(data)
	}
}

// 确保 BigEndian 无问题
func TestBigEndianAssumption(t *testing.T) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, 1)
	if buf[0] != 0 || buf[1] != 0 || buf[2] != 0 || buf[3] != 1 {
		t.Error("Go uses big-endian, this should never fail")
	}
}
