package config

import (
	"os"
	"time"

	"projectbid/server/discovery"
)

// Option 是用于配置 Application 的函数式选项。
type Option func(*Config)

// WithName 设置应用名称。
func WithName(name string) Option {
	return func(c *Config) { c.Name = name }
}

// WithDisplayName 设置可读的显示名称。
func WithDisplayName(name string) Option {
	return func(c *Config) { c.DisplayName = name }
}

// WithVersion 设置应用版本号。
func WithVersion(v string) Option {
	return func(c *Config) { c.Version = v }
}

// WithGracefulTimeout 设置优雅关闭的最大等待时间。
func WithGracefulTimeout(d time.Duration) Option {
	return func(c *Config) { c.GracefulTimeout = d }
}

// WithSignals 覆盖默认的关闭信号列表。
func WithSignals(sigs ...os.Signal) Option {
	return func(c *Config) { c.Signals = sigs }
}

// ——— 心跳 ———

// WithHeartbeatInterval 设置心跳检测间隔。
func WithHeartbeatInterval(d time.Duration) Option {
	return func(c *Config) { c.Heartbeat.Interval = d }
}

// ——— 缓冲区 ———

// WithWriteTimeout 设置写超时。
func WithWriteTimeout(d time.Duration) Option {
	return func(c *Config) { c.Buffer.WriteTimeout = d }
}

// WithMessagesBufferSize 设置消息发送缓冲区大小。
func WithMessagesBufferSize(size int) Option {
	return func(c *Config) { c.Buffer.MessagesBufferSize = size }
}

// WithLocalProcessBufferSize 设置本地消息处理缓冲区大小。
func WithLocalProcessBufferSize(size int) Option {
	return func(c *Config) { c.Buffer.LocalProcessBufferSize = size }
}

// ——— Acceptors ———

// WithAcceptorAddr 设置监听地址。
func WithAcceptorAddr(addr string) Option {
	return func(c *Config) { c.Acceptor.Addr = addr }
}

// WithAcceptorTransport 设置传输协议。
func WithAcceptorTransport(transport string) Option {
	return func(c *Config) { c.Acceptor.Transport = transport }
}

// ——— 集群 ———

// WithNatsURL 设置 NATS 连接地址。
func WithNatsURL(url string) Option {
	return func(c *Config) { c.Cluster.NatsURL = url }
}

// WithEtcd 设置 etcd 服务发现配置。
func WithEtcd(cfg discovery.EtcdConfig) Option {
	return func(c *Config) { c.Cluster.Etcd = &cfg }
}

// ——— 时间轮 ———

// WithTimeWheel 启用时间轮并设置参数。
func WithTimeWheel(tick time.Duration, wheelSize int64) Option {
	return func(c *Config) {
		c.Timer.Enabled = true
		c.Timer.Tick = tick
		c.Timer.WheelSize = wheelSize
	}
}
