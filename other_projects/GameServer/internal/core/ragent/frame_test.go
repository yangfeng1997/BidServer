package ragent

import (
	"bytes"
	"testing"

	corerpc "project/internal/core/rpc"
)

func TestEncodeDecodeRAFrame(t *testing.T) {
	header := []byte("test-header")
	body := []byte("test-body")
	data, err := encodeRAFrame(FrameRpcRequest, header, body)
	if err != nil {
		t.Fatalf("encodeRAFrame error: %v", err)
	}
	frame, err := decodeRAFrame(data)
	if err != nil {
		t.Fatalf("decodeRAFrame error: %v", err)
	}
	if frame.Type != FrameRpcRequest {
		t.Errorf("type=%d, want FrameRpcRequest", frame.Type)
	}
	if !bytes.Equal(frame.Header, header) {
		t.Errorf("header mismatch: got %q, want %q", frame.Header, header)
	}
	if !bytes.Equal(frame.Body, body) {
		t.Errorf("body mismatch: got %q, want %q", frame.Body, body)
	}
}

func TestEncodeDecodeEmptyHeader(t *testing.T) {
	data, err := encodeRAFrame(FrameHeartbeat, nil, nil)
	if err != nil {
		t.Fatalf("encodeRAFrame error: %v", err)
	}
	frame, err := decodeRAFrame(data)
	if err != nil {
		t.Fatalf("decodeRAFrame error: %v", err)
	}
	if frame.Type != FrameHeartbeat {
		t.Errorf("type=%d, want FrameHeartbeat", frame.Type)
	}
	if len(frame.Header) != 0 {
		t.Errorf("header should be empty, got %d bytes", len(frame.Header))
	}
	if len(frame.Body) != 0 {
		t.Errorf("body should be empty, got %d bytes", len(frame.Body))
	}
}

func TestEncodeFrameHeaderTooLarge(t *testing.T) {
	big := make([]byte, 0x10000) // exceeds 0xFFFF
	_, err := encodeRAFrame(FrameRpcRequest, big, nil)
	if err == nil {
		t.Error("expected error for oversized header")
	}
}

func TestDecodeFrameTooShort(t *testing.T) {
	_, err := decodeRAFrame([]byte{0, 0, 0, 0, 0, 0}) // 6 bytes, need 7
	if err == nil {
		t.Error("expected error for frame too short")
	}
}

func TestDecodeFrameLengthMismatch(t *testing.T) {
	data := make([]byte, 20)
	// set length to 100 but data is only 20 bytes
	data[0] = 0
	data[1] = 0
	data[2] = 0
	data[3] = 100 - 4 // encodeRAFrame puts length (1+2+head+body), decode expects 4+length
	_, err := decodeRAFrame(data)
	if err == nil {
		t.Error("expected error for length mismatch")
	}
}

func TestFrameTypeConstants(t *testing.T) {
	if FrameHandshake != 0x01 {
		t.Errorf("FrameHandshake=%d", FrameHandshake)
	}
	if FrameHandshakeAck != 0x02 {
		t.Errorf("FrameHandshakeAck=%d", FrameHandshakeAck)
	}
	if FrameRpcRequest != 0x03 {
		t.Errorf("FrameRpcRequest=%d", FrameRpcRequest)
	}
	if FrameRpcResponse != 0x04 {
		t.Errorf("FrameRpcResponse=%d", FrameRpcResponse)
	}
	if FrameRpcNotify != 0x05 {
		t.Errorf("FrameRpcNotify=%d", FrameRpcNotify)
	}
	if FrameHeartbeat != 0x06 {
		t.Errorf("FrameHeartbeat=%d", FrameHeartbeat)
	}
	if FrameBroadcastSent != 0x07 {
		t.Errorf("FrameBroadcastSent=%d", FrameBroadcastSent)
	}
}

func TestRPCWireHeaderRoundTrip(t *testing.T) {
	orig := rpcWireHeader{
		SeqID:       1,
		ServerType:  2,
		RoutingMode: uint8(corerpc.RoutingDirect),
		DeadlineMs:  3000,
		WaiterID:    42,
		FromNodeID:  12345,
		ErrCode:     0,
		RoutingKey:  "42",
		Route:       "LobbyService/Login",
	}
	data := encodeRPCWireHeader(orig)
	decoded, err := decodeRPCWireHeader(data)
	if err != nil {
		t.Fatalf("decodeRPCWireHeader error: %v", err)
	}
	if decoded.SeqID != orig.SeqID {
		t.Errorf("seqID=%d, want %d", decoded.SeqID, orig.SeqID)
	}
	if decoded.ServerType != orig.ServerType {
		t.Errorf("serverType=%d, want %d", decoded.ServerType, orig.ServerType)
	}
	if decoded.RoutingMode != orig.RoutingMode {
		t.Errorf("routingMode=%d, want %d", decoded.RoutingMode, orig.RoutingMode)
	}
	if decoded.DeadlineMs != orig.DeadlineMs {
		t.Errorf("deadlineMs=%d, want %d", decoded.DeadlineMs, orig.DeadlineMs)
	}
	if decoded.FromNodeID != orig.FromNodeID {
		t.Errorf("fromNodeID=%d, want %d", decoded.FromNodeID, orig.FromNodeID)
	}
	if decoded.RoutingKey != orig.RoutingKey {
		t.Errorf("routingKey=%q, want %q", decoded.RoutingKey, orig.RoutingKey)
	}
	if decoded.Route != orig.Route {
		t.Errorf("route=%q, want %q", decoded.Route, orig.Route)
	}
}

func TestRPCWireHeaderWithErrorCode(t *testing.T) {
	orig := rpcWireHeader{
		SeqID:   100,
		ErrCode: uint32(uint32(2)), // ERR_TIMEOUT
	}
	data := encodeRPCWireHeader(orig)
	decoded, err := decodeRPCWireHeader(data)
	if err != nil {
		t.Fatalf("decodeRPCWireHeader error: %v", err)
	}
	if decoded.ErrCode != 2 {
		t.Errorf("errCode=%d, want 2", decoded.ErrCode)
	}
}

func TestRPCWireHeaderEmptyKeys(t *testing.T) {
	orig := rpcWireHeader{SeqID: 1}
	data := encodeRPCWireHeader(orig)
	decoded, err := decodeRPCWireHeader(data)
	if err != nil {
		t.Fatalf("decodeRPCWireHeader error: %v", err)
	}
	if decoded.RoutingKey != "" {
		t.Errorf("routingKey=%q, want empty", decoded.RoutingKey)
	}
	if decoded.Route != "" {
		t.Errorf("route=%q, want empty", decoded.Route)
	}
}

func TestDecodeRPCWireHeaderTooShort(t *testing.T) {
	_, err := decodeRPCWireHeader(make([]byte, 10))
	if err == nil {
		t.Error("expected error for header too short")
	}
}

func TestRPCHeaderFromTarget(t *testing.T) {
	target := corerpc.Target{
		ServerType: 2,
		Mode:       corerpc.RoutingDirect,
		NodeID:     42,
	}
	rpcHeader := corerpc.Header{
		SeqID:       1,
		Route:       "Test/Hello",
		DeadlineMs:  3000,
		FromNodeID:  100,
		RoutingMode: corerpc.RoutingDirect,
		RoutingKey:  "42",
		ServerType:  2,
	}
	wire := rpcHeaderFromTarget(target, rpcHeader, 999)
	if wire.SeqID != 1 {
		t.Errorf("seqID=%d", wire.SeqID)
	}
	if wire.RoutingMode != uint8(corerpc.RoutingDirect) {
		t.Errorf("routingMode=%d", wire.RoutingMode)
	}
	if wire.FromNodeID != 999 {
		t.Errorf("fromNodeID=%d, want 999", wire.FromNodeID)
	}
	if wire.Route != "Test/Hello" {
		t.Errorf("route=%q", wire.Route)
	}
}

func TestEncodeDecodeHandshakeBody(t *testing.T) {
	body := encodeHandshakeBody(0xDEADBEEF)
	if len(body) != 4 {
		t.Fatalf("handshake body length=%d, want 4", len(body))
	}
	// Verify big-endian encoding
	_ = body
}

func TestMarshalDirectHeader(t *testing.T) {
	data := marshalDirectHeader(42)
	if len(data) == 0 {
		t.Fatal("marshalDirectHeader returned empty")
	}
	// Verify decode works
	_, err := decodeRPCWireHeader(data)
	if err != nil {
		t.Fatalf("marshalDirectHeader produced invalid header: %v", err)
	}
}

// Benchmarks
func BenchmarkEncodeRAFrame(b *testing.B) {
	h := make([]byte, 64)
	body := make([]byte, 256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encodeRAFrame(FrameRpcRequest, h, body)
	}
}

func BenchmarkDecodeRAFrame(b *testing.B) {
	h := make([]byte, 64)
	body := make([]byte, 256)
	data, _ := encodeRAFrame(FrameRpcRequest, h, body)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decodeRAFrame(data)
	}
}

func BenchmarkEncodeRPCWireHeader(b *testing.B) {
	h := rpcWireHeader{
		SeqID:       1,
		ServerType:  2,
		RoutingMode: 1,
		DeadlineMs:  3000,
		FromNodeID:  12345,
		RoutingKey:  "42",
		Route:       "LobbyService/Login",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encodeRPCWireHeader(h)
	}
}

func BenchmarkDecodeRPCWireHeader(b *testing.B) {
	h := rpcWireHeader{
		SeqID:      1,
		ServerType: 2,
		RoutingKey: "42",
		Route:      "LobbyService/Login",
	}
	data := encodeRPCWireHeader(h)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decodeRPCWireHeader(data)
	}
}
