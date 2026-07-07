package acceptor

import (
	"net"
	"testing"
	"time"
)

// TestTCPAcceptor_ListenFailureGraceful 验证 listen 失败时不 panic，
// 而是关闭 connChan 并返回（对齐 WSAcceptor 行为）。
// 修复前：net.Listen 失败 → panic（在独立 goroutine 中无法 recover，崩整进程）。
func TestTCPAcceptor_ListenFailureGraceful(t *testing.T) {
	// 先占住一个端口，令同 addr 的 acceptor listen 必然失败
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to occupy port: %v", err)
	}
	defer busy.Close()

	a := NewTCPAcceptor(busy.Addr().String())

	// ListenAndServe 在 listen 失败时应立即返回（非阻塞）且不 panic。
	// recover 把修复前的 panic 转成干净的测试失败（而非崩溃测试二进制）。
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if rec := recover(); rec != nil {
				t.Errorf("ListenAndServe panicked on listen failure: %v", rec)
			}
		}()
		a.ListenAndServe()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return on listen failure")
	}

	if a.IsRunning() {
		t.Fatal("acceptor should not be running after listen failure")
	}

	// connChan 应已被关闭（非阻塞校验，避免修复前未关闭导致测试 hang）
	select {
	case _, ok := <-a.ConnChan():
		if ok {
			t.Fatal("connChan should be closed, but received a connection")
		}
	case <-time.After(time.Second):
		t.Fatal("connChan was not closed after listen failure")
	}
}
