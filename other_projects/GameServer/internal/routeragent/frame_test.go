package routeragent

import "testing"

func TestEncodeDecodeFrame(t *testing.T) {
	f := Frame{Type: FrameRpcRequest, Header: []byte("h"), Body: []byte("b")}
	data, err := EncodeFrame(f)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	decoded, err := DecodeFrame(data)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if decoded.Type != FrameRpcRequest {
		t.Errorf("type=%d", decoded.Type)
	}
	if string(decoded.Header) != "h" {
		t.Errorf("header=%q", decoded.Header)
	}
	if string(decoded.Body) != "b" {
		t.Errorf("body=%q", decoded.Body)
	}
}

func TestEncodeDecodeRouteBody(t *testing.T) {
	data := EncodeRouteBody(42, []byte("hello"))
	nodeID, payload, err := DecodeRouteBody(data)
	if err != nil {
		t.Fatalf("DecodeRouteBody: %v", err)
	}
	if nodeID != 42 {
		t.Errorf("nodeID=%d", nodeID)
	}
	if string(payload) != "hello" {
		t.Errorf("payload=%q", payload)
	}
}
