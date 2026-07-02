package rpc

import "testing"

func TestInit(t *testing.T) {
	Init(nil)
	if Lobby == nil || Gate == nil {
		t.Fatal("stubs should be initialized")
	}
}
