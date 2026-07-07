// Package constants 定义应用级常量与哨兵错误。
package constants

import "errors"

// Context 键类型，用于在 context 中存储框架级数据。
type contextKey string

// Context 键常量。
const (
	StartTimeKey  contextKey = "pitaya.startTime"
	RouteKey      contextKey = "pitaya.route"
	SessionCtxKey contextKey = "pitaya.session"
	LoggerCtxKey  contextKey = "pitaya.logger"
	RequestIDKey  contextKey = "pitaya.requestId"
	PeerIDKey     contextKey = "pitaya.peerId"
	PeerServiceKey contextKey = "pitaya.peerService"
	IPVersionKey  string     = "ipVersion"
)

// 应用生命周期哨兵错误。
var (
	// ErrAlreadyStarted 表示 Start 被重复调用。
	ErrAlreadyStarted = errors.New("应用已启动")

	// ErrAlreadyStopped 表示 Application 已经停止。
	ErrAlreadyStopped = errors.New("应用已停止")

	// ErrNotRunning 表示在应用启动前尝试关闭。
	ErrNotRunning = errors.New("应用未运行")

	// ErrGracefulTimeout 表示优雅关闭超时。
	ErrGracefulTimeout = errors.New("优雅关闭超时")

	// ErrDuplicateName 表示注册了重复的组件名称。
	ErrDuplicateName = errors.New("组件名称重复")

	// ErrReplyShouldBeNotNull 表示 handler 返回值不应为 nil。
	ErrReplyShouldBeNotNull = errors.New("回复不应为 nil")

	// ErrConnectionClosed 表示连接已关闭。
	ErrConnectionClosed = errors.New("客户端连接已关闭")

	// ErrTimeout 表示操作超时。
	ErrTimeout = errors.New("操作超时")

	// ErrNotFound 表示目标未找到。
	ErrNotFound = errors.New("未找到")

	// ErrInternal 表示内部错误。
	ErrInternal = errors.New("内部错误")
)
// Agent 连接状态常量（对齐 Pitaya 的 agent 状态定义）。
const (
	// StatusStart 连接刚建立，等待握手。
	StatusStart int32 = iota + 1
	// StatusHandshake 已发送握手响应，等待客户端确认。
	StatusHandshake
	// StatusWorking 正常工作状态。
	StatusWorking
	// StatusClosed 已关闭。
	StatusClosed
)

