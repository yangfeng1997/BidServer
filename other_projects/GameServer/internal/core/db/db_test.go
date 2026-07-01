package db

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolStartStop(t *testing.T) {
	p := NewPool(2)
	p.Start()
	p.Stop()
}

func TestPoolSubmit(t *testing.T) {
	p := NewPool(2)
	p.Start()
	defer p.Stop()

	var called atomic.Bool
	done := make(chan struct{})
	p.Submit(Job{
		Key: "test",
		Fn: func() (any, error) {
			return "result", nil
		},
		Done: func(result any, err error) {
			if result != "result" {
				t.Errorf("result=%v, want result", result)
			}
			if err != nil {
				t.Errorf("err=%v, want nil", err)
			}
			called.Store(true)
			close(done)
		},
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for job")
	}
	if !called.Load() {
		t.Error("callback was not called")
	}
}

func TestPoolMultipleWorkers(t *testing.T) {
	p := NewPool(4)
	p.Start()
	defer p.Stop()

	var count atomic.Int32
	done := make(chan struct{})
	n := 20
	for i := 0; i < n; i++ {
		p.Submit(Job{
			Key: "multi",
			Fn: func() (any, error) {
				time.Sleep(10 * time.Millisecond)
				return nil, nil
			},
			Done: func(any, error) {
				if count.Add(1) == int32(n) {
					close(done)
				}
			},
		})
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for all jobs")
	}
	if count.Load() != int32(n) {
		t.Errorf("count=%d, want %d", count.Load(), n)
	}
}

func TestPoolSubmitAfterStop(t *testing.T) {
	p := NewPool(1)
	p.Start()
	p.Stop()

	var errCalled atomic.Bool
	p.Submit(Job{
		Key: "late",
		Done: func(_ any, err error) {
			if err != ErrPoolClosed {
				t.Errorf("err=%v, want ErrPoolClosed", err)
			}
			errCalled.Store(true)
		},
	})
	if !errCalled.Load() {
		t.Error("ErrPoolClosed not returned")
	}
}

func TestPoolDefaultWorkers(t *testing.T) {
	p := NewPool(0)
	if p.workers != 4 {
		t.Errorf("default workers=%d, want 4", p.workers)
	}
}

func TestModuleAsync(t *testing.T) {
	// 创建模块（不使用 app，手动模拟主循环）
	m := NewModule(2)
	m.poster = &fakePoster{}

	m.AfterInit()
	defer m.BeforeStop()

	var got atomic.Value
	done := make(chan struct{})
	m.Async("test", func() (any, error) {
		time.Sleep(10 * time.Millisecond)
		return 42, nil
	}, func(result any, err error) {
		got.Store(result)
		close(done)
	})

	// drain poster 模拟主循环
	if fp, ok := m.poster.(*fakePoster); ok {
		time.Sleep(100 * time.Millisecond)
		fp.drain()
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
	if got.Load() != 42 {
		t.Errorf("result=%v, want 42", got.Load())
	}
}

type fakePoster struct {
	mu  sync.Mutex
	fns []func()
}

func (p *fakePoster) Post(fn func()) {
	p.mu.Lock()
	p.fns = append(p.fns, fn)
	p.mu.Unlock()
}

func (p *fakePoster) drain() {
	p.mu.Lock()
	fns := p.fns
	p.fns = nil
	p.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
