// Package health 提供健康检查与就绪探测 HTTP 端点，适配 K8s/Docker 部署。
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ——— 状态类型 ———

// Status 表示组件健康状态。
type Status string

const (
	// StatusUp 表示组件正常运行。
	StatusUp Status = "UP"
	// StatusDown 表示组件不可用。
	StatusDown Status = "DOWN"
)

// ——— 检查器接口 ———

// Checker 定义健康检查逻辑。
type Checker interface {
	Name() string
	Check(ctx context.Context) Status
}

// CheckerFunc 将函数适配为 Checker 接口。
type CheckerFunc struct {
	name string
	fn   func(ctx context.Context) Status
}

func (c *CheckerFunc) Name() string                  { return c.name }
func (c *CheckerFunc) Check(ctx context.Context) Status { return c.fn(ctx) }

// NewCheckerFunc 创建基于函数的检查器。
func NewCheckerFunc(name string, fn func(ctx context.Context) Status) Checker {
	return &CheckerFunc{name: name, fn: fn}
}

// ——— Server ———

// Server 运行健康检查 HTTP 端点。
type Server struct {
	addr    string
	handler *Handler
	server  *http.Server
	running int32
}

// Handler 管理多个 Checker 并暴露 HTTP 端点。
type Handler struct {
	mu       sync.RWMutex
	checkers map[string]Checker
	ready    int32
}

// NewHandler 创建健康检查处理器。
func NewHandler() *Handler {
	return &Handler{
		checkers: make(map[string]Checker),
	}
}

// Register 注册健康检查器。
func (h *Handler) Register(c Checker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checkers[c.Name()] = c
}

// SetReady 标记服务为就绪状态。
func (h *Handler) SetReady(ready bool) {
	if ready {
		atomic.StoreInt32(&h.ready, 1)
	} else {
		atomic.StoreInt32(&h.ready, 0)
	}
}

// IsReady 返回服务是否就绪。
func (h *Handler) IsReady() bool {
	return atomic.LoadInt32(&h.ready) == 1
}

// NewServer 创建健康检查服务器。
// addr 格式如 ":8080"。
func NewServer(addr string, handler *Handler) *Server {
	if addr == "" {
		addr = ":8080"
	}
	if handler == nil {
		handler = NewHandler()
	}
	return &Server{
		addr:    addr,
		handler: handler,
	}
}

// Start 启动健康检查 HTTP 服务器。
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handler.ServeHealth)
	mux.HandleFunc("/ready", s.handler.ServeReady)
	mux.HandleFunc("/status", s.handler.ServeStatus)

	atomic.StoreInt32(&s.running, 1)
	s.server = &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
			atomic.StoreInt32(&s.running, 0)
		}
	}()

	return nil
}

// Stop 优雅停止健康检查服务器。
func (s *Server) Stop() error {
	atomic.StoreInt32(&s.running, 0)
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}
	return nil
}

// ——— HTTP 处理器 ———

// ServeHealth 处理 /health 请求。执行所有检查器并返回汇总状态。
func (h *Handler) ServeHealth(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	type checkResult struct {
		Name   string `json:"name"`
		Status Status `json:"status"`
	}

	results := make([]checkResult, 0, len(h.checkers))
	overallUp := true

	for _, c := range h.checkers {
		status := c.Check(ctx)
		results = append(results, checkResult{Name: c.Name(), Status: status})
		if status != StatusUp {
			overallUp = false
		}
	}

	status := StatusUp
	httpStatus := http.StatusOK
	if !overallUp {
		status = StatusDown
		httpStatus = http.StatusServiceUnavailable
	}

	resp := map[string]interface{}{
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"checks":    results,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(resp)
}

// ServeReady 处理 /ready 请求，用于 K8s readiness probe。
func (h *Handler) ServeReady(w http.ResponseWriter, r *http.Request) {
	if h.IsReady() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready"})
	}
}

// ServeStatus 处理 /status 请求，返回简单文本状态。
func (h *Handler) ServeStatus(w http.ResponseWriter, r *http.Request) {
	status := "UP"
	if !h.IsReady() {
		status = "NOT READY"
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(status))
}
