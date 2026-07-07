package message

import (
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	// 设置路由字典
	SetDictionary(map[string]uint16{"Room.Join": 1})

	enc := NewMessagesEncoder(false)
	original := &Message{Type: Request, ID: 1, Route: "Room.Join", Data: []byte(`{"name":"test"}`)}

	data, err := enc.Encode(original)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode 失败: %v", err)
	}

	if decoded.Type != original.Type {
		t.Errorf("Type 不匹配: %d != %d", decoded.Type, original.Type)
	}
	if decoded.ID != original.ID {
		t.Errorf("ID 不匹配: %d != %d", decoded.ID, original.ID)
	}
	if decoded.Route != original.Route {
		t.Errorf("Route 不匹配: %s != %s", decoded.Route, original.Route)
	}
	if string(decoded.Data) != string(original.Data) {
		t.Errorf("Data 不匹配: %s != %s", decoded.Data, original.Data)
	}
}

func TestEncodeWithCompression(t *testing.T) {
	SetDictionary(map[string]uint16{"Test.Ping": 1})

	enc := NewMessagesEncoder(true)
	payload := make([]byte, 2048)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	original := &Message{Type: Request, ID: 1, Route: "Test.Ping", Data: payload}
	data, err := enc.Encode(original)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode 失败: %v", err)
	}
	if len(decoded.Data) != len(payload) {
		t.Errorf("压缩往返后数据长度不匹配: %d != %d", len(decoded.Data), len(payload))
	}
}

func TestNotifyEncodeDecode(t *testing.T) {
	enc := NewMessagesEncoder(false)
	original := &Message{Type: Notify, Route: "Test.Echo", Data: []byte("hello")}

	data, err := enc.Encode(original)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode 失败: %v", err)
	}
	if decoded.Type != Notify {
		t.Errorf("expected Notify, got %d", decoded.Type)
	}
}

func TestResponseEncodeDecode(t *testing.T) {
	enc := NewMessagesEncoder(false)
	original := &Message{Type: Response, ID: 42, Data: []byte("ok")}

	data, err := enc.Encode(original)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode 失败: %v", err)
	}
	if decoded.Type != Response {
		t.Errorf("expected Response, got %d", decoded.Type)
	}
	if decoded.ID != 42 {
		t.Errorf("ID = %d, want 42", decoded.ID)
	}
}

func TestErrorFlag(t *testing.T) {
	enc := NewMessagesEncoder(false)
	original := &Message{Type: Response, ID: 1, Err: true, Data: []byte("error message")}

	data, err := enc.Encode(original)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode 失败: %v", err)
	}
	if !decoded.Err {
		t.Error("Err 标记应保持")
	}
}

func TestDecodeInvalidData(t *testing.T) {
	_, err := Decode([]byte{})
	if err == nil {
		t.Error("空数据应返回错误")
	}
}
