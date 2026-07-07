package rpc

import "testing"

func TestInit(t *testing.T) {
	Init(nil)
	if Lobby == nil || Room == nil || Match == nil || Online == nil || Gate == nil {
		t.Fatal("stubs should be initialized")
	}
}
