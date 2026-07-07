package application

import "fmt"

// State 表示 Application 的当前生命周期阶段。
type State int

const (
	// StateInitializing 表示组件正在初始化。
	StateInitializing State = iota
	// StateRunning 表示应用正在运行。
	StateRunning
	// StateStopping 表示正在关闭。
	StateStopping
	// StateStopped 表示已完全停止。
	StateStopped
)

func (s State) String() string {
	switch s {
	case StateInitializing:
		return "初始化中"
	case StateRunning:
		return "运行中"
	case StateStopping:
		return "关闭中"
	case StateStopped:
		return "已停止"
	default:
		return fmt.Sprintf("未知(%d)", s)
	}
}
