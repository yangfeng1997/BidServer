package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckerFunc(t *testing.T) {
	called := false
	c := NewCheckerFunc("test", func(ctx context.Context) Status {
		called = true
		return StatusUp
	})
	if c.Name() != "test" {
		t.Errorf("Name() = %s, want test", c.Name())
	}
	if s := c.Check(context.Background()); s != StatusUp {
		t.Errorf("Check() = %s, want UP", s)
	}
	if !called {
		t.Error("Check 未被调用")
	}
}

func TestHandlerReady(t *testing.T) {
	h := NewHandler()
	if h.IsReady() {
		t.Error("未就绪时 IsReady 应返回 false")
	}
	h.SetReady(true)
	if !h.IsReady() {
		t.Error("就绪后 IsReady 应返回 true")
	}
}

func TestServeStatus(t *testing.T) {
	h := NewHandler()
	h.SetReady(true)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	h.ServeStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d, want 200", w.Code)
	}
	if w.Body.String() != "UP" {
		t.Errorf("body = %s, want UP", w.Body.String())
	}
}

func TestServeHealth(t *testing.T) {
	h := NewHandler()
	h.Register(NewCheckerFunc("db", func(ctx context.Context) Status { return StatusUp }))
	h.Register(NewCheckerFunc("redis", func(ctx context.Context) Status { return StatusDown }))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHealth(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("有 DOWN 检查器时状态码 = %d, want 503", w.Code)
	}
}

func TestServeReady(t *testing.T) {
	h := NewHandler()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	h.ServeReady(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Error("未就绪时应返回 503")
	}

	h.SetReady(true)
	w = httptest.NewRecorder()
	h.ServeReady(w, req)
	if w.Code != http.StatusOK {
		t.Error("就绪时应返回 200")
	}
}
