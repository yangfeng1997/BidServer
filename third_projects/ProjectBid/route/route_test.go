package route

import (
	"testing"
)

func TestDecodeFullRoute(t *testing.T) {
	r, err := Decode("RoomServer.Room.Join")
	if err != nil {
		t.Fatalf("解析全路由失败: %v", err)
	}
	if r.SvType != "RoomServer" {
		t.Errorf("SvType = %s, want RoomServer", r.SvType)
	}
	if r.Service != "Room" {
		t.Errorf("Service = %s, want Room", r.Service)
	}
	if r.Method != "Join" {
		t.Errorf("Method = %s, want Join", r.Method)
	}
}

func TestDecodeShortRoute(t *testing.T) {
	r, err := Decode("Room.Join")
	if err != nil {
		t.Fatalf("解析短路失败: %v", err)
	}
	if r.SvType != "" {
		t.Errorf("expected empty SvType, got %s", r.SvType)
	}
	if r.Service != "Room" {
		t.Errorf("Service = %s, want Room", r.Service)
	}
}

func TestDecodeInvalidRoutes(t *testing.T) {
	tests := []string{"", "Room", "Room.", "Room...Join"}
	for _, rt := range tests {
		_, err := Decode(rt)
		if err == nil {
			t.Errorf("expected error for route %q", rt)
		}
	}
}

func TestRouteString(t *testing.T) {
	r := &Route{SvType: "Chat", Service: "Room", Method: "Join"}
	s := r.String()
	if s != "Chat.Room.Join" {
		t.Errorf("String() = %s, want Chat.Room.Join", s)
	}
}

func TestRouteShort(t *testing.T) {
	r := &Route{SvType: "Chat", Service: "Room", Method: "Join"}
	if s := r.Short(); s != "Room.Join" {
		t.Errorf("Short() = %s, want Room.Join", s)
	}
}
