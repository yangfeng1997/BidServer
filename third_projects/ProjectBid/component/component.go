package component

import "context"

// Component 定义 Application 管理的组件的生命周期钩子。
// 调用顺序：
//
//	Init → AfterInit → （应用运行中） → BeforeShutdown → Shutdown
//
// 每个方法接收一个 context，在阶段超时时会被取消。
// 实现者必须尊重 context 的取消信号。
type Component interface {
	// Name 返回组件的唯一标识符。
	Name() string

	// Init 执行初始化。此时所有组件已注册，可以安全地进行跨组件查找。
	Init(ctx context.Context) error

	// AfterInit 在所有组件完成 Init 后调用。
	// 用于需要其他组件已完全初始化的跨组件连接。
	AfterInit(ctx context.Context) error

	// BeforeShutdown 在关闭开始时、任何 Shutdown 方法执行之前调用。
	// 用于停止接收新请求。
	BeforeShutdown(ctx context.Context) error

	// Shutdown 执行最终的资源清理和释放。
	Shutdown(ctx context.Context) error
}
