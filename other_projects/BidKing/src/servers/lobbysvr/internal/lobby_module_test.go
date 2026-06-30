package internal

import (
	"testing"
	"time"
)

func TestLobbyModule_StartStop(t *testing.T) {
	rt, _ := newTestRuntimeWithStore(newFakeStore())
	m := NewLobbyModule(rt)
	if m.Name() != "lobby" {
		t.Fatalf("name=%s", m.Name())
	}
	m.Init() // 启动 loop
	// loop 可处理 Submit
	done := make(chan struct{})
	rt.Submit(func() { close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop not running after Init")
	}
	m.OnStop() // 停 loop（不应 panic / 阻塞）
}
