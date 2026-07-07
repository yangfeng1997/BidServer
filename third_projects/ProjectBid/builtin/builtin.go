// Package builtin 提供 ping、服务器信息等内置 handler 方法。
// 通过 Builder.Register 注册后，客户端即可调用这些通用 RPC。
package builtin

import (
	"context"
	"runtime"
	"sync/atomic"
	"time"

	"projectbid/server/component"
)

// ServerInfo 服务器元信息，由应用在创建 BuiltinHandlers 时注入。
type ServerInfo struct {
	Name      string
	Version   string
	ServerID  string
	IsFrontend bool
}

// BuiltinHandlers 提供通用 RPC handler 方法。
// 嵌入 component.Base 以获得默认生命周期实现，通过反射自动发现所有导出方法。
type BuiltinHandlers struct {
	component.Base
	info      ServerInfo
	startTime time.Time
	msgCount  int64
}

// Name 返回组件名称。
func (h *BuiltinHandlers) Name() string { return "BuiltinHandlers" }

// NewBuiltinHandlers 创建内置 handler 组件。
func NewBuiltinHandlers(info ServerInfo) *BuiltinHandlers {
	return &BuiltinHandlers{
		info:      info,
		startTime: time.Now(),
	}
}

// ——— 请求/响应类型 ———

// PingReq ping 请求（可为空）。
type PingReq struct{}

// PingResp pong 响应。
type PingResp struct {
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// ServerInfoReq 服务器信息请求（可为空）。
type ServerInfoReq struct{}

// ServerInfoResp 服务器信息响应。
type ServerInfoResp struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	ServerID   string `json:"serverId"`
	IsFrontend bool   `json:"isFrontend"`
	Uptime     string `json:"uptime"`
	GoVersion  string `json:"goVersion"`
	NumCPU     int    `json:"numCPU"`
	NumGoroutine int  `json:"numGoroutine"`
}

// StatsReq 统计信息请求（可为空）。
type StatsReq struct{}

// StatsResp 统计信息响应。
type StatsResp struct {
	MessageCount int64  `json:"messageCount"`
	Uptime       string `json:"uptime"`
}

// ——— Handler 方法 ———

// Ping 健康检查，返回 pong。
func (h *BuiltinHandlers) Ping(ctx context.Context, req *PingReq) (*PingResp, error) {
	return &PingResp{
		Message:   "pong",
		Timestamp: time.Now().UnixMilli(),
	}, nil
}

// ServerInfo 返回服务器基本信息。
func (h *BuiltinHandlers) ServerInfo(ctx context.Context, req *ServerInfoReq) (*ServerInfoResp, error) {
	return &ServerInfoResp{
		Name:         h.info.Name,
		Version:      h.info.Version,
		ServerID:     h.info.ServerID,
		IsFrontend:   h.info.IsFrontend,
		Uptime:       time.Since(h.startTime).String(),
		GoVersion:    runtime.Version(),
		NumCPU:       runtime.NumCPU(),
		NumGoroutine: runtime.NumGoroutine(),
	}, nil
}

// Stats 返回简单的运行时统计。
func (h *BuiltinHandlers) Stats(ctx context.Context, req *StatsReq) (*StatsResp, error) {
	return &StatsResp{
		MessageCount: atomic.LoadInt64(&h.msgCount),
		Uptime:       time.Since(h.startTime).String(),
	}, nil
}

// OnMessage 外部可在消息处理时调用以更新内部计数。
func (h *BuiltinHandlers) OnMessage() {
	atomic.AddInt64(&h.msgCount, 1)
}
