package handler

import (
	"context"
	"testing"

	"project/src/common/serialize/json"
)

func testSerializer() *json.Serializer { return json.NewSerializer() }

// PingHandler 仅用于 registry 单测的最小合法 handler
type PingHandler struct{}

type pingResp struct{}

func (*PingHandler) Ping(_ context.Context) (*pingResp, error) { return &pingResp{}, nil }

func TestRegistry_HasRoute(t *testing.T) {
	r := NewRegistry(testSerializer())
	if err := r.RegisterHandler(&PingHandler{}, nil); err != nil {
		t.Fatal(err)
	}
	if !r.HasRoute("PingHandler.ping") {
		t.Fatal("expected route registered")
	}
	if r.HasRoute("PingHandler.nope") {
		t.Fatal("unexpected route")
	}
}
