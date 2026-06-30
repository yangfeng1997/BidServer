package module

// Module 可插拔子系统接口
type Module interface {
	// Name 模块唯一标识，用于 Find/Remove
	Name() string
	// Init 初始化，此时其他模块已注册但尚未 Init
	Init()
	// OnAfterInit 所有模块 Init 完成后调用，可安全使用其他模块
	OnAfterInit()
	// OnBeforeStop 收到停止信号后首先调用，用于停止接收新请求
	OnBeforeStop()
	// OnStop 实际停止，释放资源
	OnStop()
}
