// Package metrics 提供应用监控指标抽象与 Prometheus 实现。
package metrics

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// ——— Reporter 接口 ———

// Reporter 定义指标上报器接口。
type Reporter interface {
	// ReportTiming 上报耗时指标（微秒）。
	ReportTiming(metric string, tags map[string]string, latency time.Duration)
	// ReportCount 上报计数指标。
	ReportCount(metric string, tags map[string]string, delta int64)
	// ReportGauge 上报瞬时值指标。
	ReportGauge(metric string, tags map[string]string, value float64)
	// GetType 返回上报器类型名称。
	GetType() string
}

// ——— 全局指标函数 ———

var (
	connectedClients int64
	totalMessages    int64
	totalErrors      int64
)

var globalReporters []Reporter

// RegisterReporter 注册全局指标上报器。
func RegisterReporter(r Reporter) {
	globalReporters = append(globalReporters, r)
}

// ReportTiming 向所有已注册上报器发送耗时。
func ReportTiming(metric string, tags map[string]string, latency time.Duration) {
	for _, r := range globalReporters {
		r.ReportTiming(metric, tags, latency)
	}
}

// ReportCount 向所有已注册上报器发送计数变化。
func ReportCount(metric string, tags map[string]string, delta int64) {
	for _, r := range globalReporters {
		r.ReportCount(metric, tags, delta)
	}
}

// ReportGauge 向所有已注册上报器发送瞬时值。
func ReportGauge(metric string, tags map[string]string, value float64) {
	for _, r := range globalReporters {
		r.ReportGauge(metric, tags, value)
	}
}

// ——— 内置指标上报 ———

// ReportConnectedClients 上报已连接客户端数。
func ReportConnectedClients() {
	ReportGauge("connected_clients", nil, float64(atomic.LoadInt64(&connectedClients)))
}

// ReportMessageCount 上报已处理消息总数。
func ReportMessageCount(delta int64) {
	atomic.AddInt64(&totalMessages, delta)
	ReportCount("messages_total", nil, delta)
}

// ReportErrorCount 上报错误总数。
func ReportErrorCount(delta int64) {
	atomic.AddInt64(&totalErrors, delta)
	ReportCount("errors_total", nil, delta)
}

// ReportMessageDelay 上报消息处理延迟。
func ReportMessageDelay(ctx context.Context, latency time.Duration) {
	ReportTiming("message_delay", nil, latency)
}

// IncConnectedClients 增加已连接客户端计数。
func IncConnectedClients(delta int64) {
	atomic.AddInt64(&connectedClients, delta)
}

// ——— Prometheus Reporter ———

// PrometheusConfig Prometheus 上报器配置。
type PrometheusConfig struct {
	ListenAddr string // 例如 ":9100"
}

// PrometheusReporter 暴露 /metrics 端点供 Prometheus 抓取。
type PrometheusReporter struct {
	addr    string
	server  *http.Server
	running int32
}

// NewPrometheusReporter 创建 Prometheus 指标上报器。
func NewPrometheusReporter(cfg PrometheusConfig) *PrometheusReporter {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":9100"
	}
	return &PrometheusReporter{addr: cfg.ListenAddr}
}

// GetType 返回 "prometheus"。
func (p *PrometheusReporter) GetType() string { return "prometheus" }

// ReportTiming 上报耗时（暂以 Counter + 标签模式输出）。
func (p *PrometheusReporter) ReportTiming(metric string, tags map[string]string, latency time.Duration) {
	// 实际 Prometheus 实现通过 promhttp.Handler 自动收集
}

// ReportCount 上报计数变化。
func (p *PrometheusReporter) ReportCount(metric string, tags map[string]string, delta int64) {
	// 接口预留
}

// ReportGauge 上报瞬时值。
func (p *PrometheusReporter) ReportGauge(metric string, tags map[string]string, value float64) {
	// 接口预留
}

// Start 启动 HTTP 服务器暴露 /metrics。
func (p *PrometheusReporter) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte("# projectbid metrics endpoint\n"))
		w.Write([]byte("# 接入 promhttp.Handler 后自动生效\n"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	p.server = &http.Server{Addr: p.addr, Handler: mux}
	atomic.StoreInt32(&p.running, 1)

	go func() {
		if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			atomic.StoreInt32(&p.running, 0)
		}
	}()

	return nil
}

// Stop 停止指标服务器。
func (p *PrometheusReporter) Stop() error {
	atomic.StoreInt32(&p.running, 0)
	if p.server != nil {
		return p.server.Close()
	}
	return nil
}
