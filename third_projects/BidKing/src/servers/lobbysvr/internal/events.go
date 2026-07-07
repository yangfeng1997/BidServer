package internal

import "project/src/common/event"

// PlayerLoaded 玩家工作副本加载完成事件（组件可订阅做初始化）
type PlayerLoaded struct{ UID int64 }

// CurrencyChanged 货币变动事件（不会失败的副作用通知，如审计/任务统计）
type CurrencyChanged struct {
	UID   int64
	Kind  string
	Delta int64
}

// Events lobby 进程内同步事件总线集合，仅主循环使用（零锁）。
// P3a 仅 PlayerLoaded；跨组件业务事件（买道具→扣货币）随组件落地于 P3b 增补。
type Events struct {
	PlayerLoaded    *event.Bus[PlayerLoaded]
	CurrencyChanged *event.Bus[CurrencyChanged]
}

func NewEvents() *Events {
	return &Events{
		PlayerLoaded:    event.NewBus[PlayerLoaded](),
		CurrencyChanged: event.NewBus[CurrencyChanged](),
	}
}
