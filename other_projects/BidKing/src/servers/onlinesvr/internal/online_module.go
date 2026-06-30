package internal

import (
	"time"

	"project/src/common/logger"
	"project/src/common/timewheel"
	"project/src/framework/module"
)

// 在线过期 TTL（= 5min 重连宽限窗口雏形）。可通过 NewOnlineModule 注入便于测试。
const DefaultEntryTTL = 5 * time.Minute

// OnlineModule onlinesvr 生命周期：持有 timewheel 与 Directory，
// Init 启动自驱动时间轮，OnStop 关闭。
type OnlineModule struct {
	module.BaseModule
	tw  *timewheel.TimeWheel
	dir *Directory
}

func NewOnlineModule(ttl time.Duration) *OnlineModule {
	tw := timewheel.New(time.Second, 512) // 单圈 512s，覆盖 5min ttl
	return &OnlineModule{tw: tw, dir: NewDirectory(tw, ttl)}
}

func (m *OnlineModule) Name() string         { return "online" }
func (m *OnlineModule) Directory() *Directory { return m.dir }

func (m *OnlineModule) Init() {
	m.tw.Start()
	logger.Info("online module initialized", logger.Int("entryTTLsec", int(m.dir.ttl.Seconds())))
}

func (m *OnlineModule) OnStop() {
	m.tw.Close()
	logger.Info("online module stopped")
}
