// Package config 包含 Application 的配置设置、函数式选项和 Viper 加载。
package config

import (
	"os"
	"syscall"
	"time"

	"projectbid/server/discovery"
)

// Config 包含 Application 的完整层次化配置。
type Config struct {
	// Name 是应用实例的唯一标识符，用于日志和服务发现。默认："app"
	Name string

	// DisplayName 是日志中显示的可读名称。默认：Name
	DisplayName string

	// Version 是应用版本号。默认："0.0.0"
	Version string

	// GracefulTimeout 是优雅关闭的最大等待时间。默认：30秒
	GracefulTimeout time.Duration

	// Signals 是触发优雅关闭的操作系统信号列表。
	Signals []os.Signal

	// Heartbeat 心跳相关配置。
	Heartbeat HeartbeatConfig

	// Buffer 缓冲区相关配置。
	Buffer BufferConfig

	// Acceptor 网络监听相关配置。
	Acceptor AcceptorConfig

	// Cluster 集群通信相关配置。
	Cluster ClusterConfig

	// Timer 时间轮相关配置。
	Timer TimerConfig
}

// HeartbeatConfig 心跳相关配置。
type HeartbeatConfig struct {
	// Interval 心跳检测间隔。默认：30秒
	Interval time.Duration
}

// BufferConfig 缓冲区相关配置。
type BufferConfig struct {
	// WriteTimeout 写超时。默认：15秒
	WriteTimeout time.Duration

	// MessagesBufferSize 消息发送缓冲区大小。默认：256
	MessagesBufferSize int

	// LocalProcessBufferSize 本地消息处理缓冲区大小。默认：100
	LocalProcessBufferSize int
}

// AcceptorConfig 网络监听相关配置。
type AcceptorConfig struct {
	// Addr 监听地址。默认：":8080"
	Addr string

	// Transport 传输协议。默认："tcp"
	Transport string
}

// ClusterConfig 集群通信相关配置。
type ClusterConfig struct {
	// NatsURL NATS 连接地址。为空表示不使用集群。
	NatsURL string

	// Etcd etcd 服务发现配置。
	Etcd *discovery.EtcdConfig
}

// TimerConfig 时间轮相关配置。
type TimerConfig struct {
	// Enabled 是否启用时间轮。默认：false
	Enabled bool

	// Tick 时间轮滴答间隔。默认：10ms
	Tick time.Duration

	// WheelSize 时间轮槽数。默认：20
	WheelSize int64
}

// Default 返回带有合理默认值的 Config。
func Default() Config {
	return Config{
		Name:            "app",
		DisplayName:     "",
		Version:         "0.0.0",
		GracefulTimeout: 30 * time.Second,
		Signals:         []os.Signal{os.Interrupt, syscall.SIGTERM},
		Heartbeat: HeartbeatConfig{
			Interval: 30 * time.Second,
		},
		Buffer: BufferConfig{
			WriteTimeout:          15 * time.Second,
			MessagesBufferSize:    256,
			LocalProcessBufferSize: 100,
		},
		Acceptor: AcceptorConfig{
			Addr:      ":8080",
			Transport: "tcp",
		},
		Timer: TimerConfig{
			Enabled:    false,
			Tick:       10 * time.Millisecond,
			WheelSize:  20,
		},
	}
}
