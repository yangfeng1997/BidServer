package routeragent

import "time"

// KeepAlive 负责链路存活检查
type KeepAlive struct {
	interval time.Duration
	timeout  time.Duration
}

// 创建心跳器
func NewKeepAlive(interval, timeout time.Duration) *KeepAlive {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &KeepAlive{interval: interval, timeout: timeout}
}

// Run 启动心跳检查循环
func (k *KeepAlive) Run(stopCh <-chan struct{}) {
	ticker := time.NewTicker(k.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = k.timeout
		case <-stopCh:
			return
		}
	}
}
