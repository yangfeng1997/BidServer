package config

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/viper"

	"projectbid/server/discovery"
)

// NewConfigFromViper 从已配置好的 Viper 实例构建 Config。
func NewConfigFromViper(v *viper.Viper) (Config, error) {
	fillDefaultValues(v)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	cfg := Config{
		Name:            v.GetString("name"),
		DisplayName:     v.GetString("displayName"),
		Version:         v.GetString("version"),
		GracefulTimeout: v.GetDuration("gracefulTimeout"),
		Heartbeat: HeartbeatConfig{
			Interval: v.GetDuration("heartbeat.interval"),
		},
		Buffer: BufferConfig{
			WriteTimeout:          v.GetDuration("buffer.writeTimeout"),
			MessagesBufferSize:    v.GetInt("buffer.messagesBufferSize"),
			LocalProcessBufferSize: v.GetInt("buffer.localProcessBufferSize"),
		},
		Acceptor: AcceptorConfig{
			Addr:      v.GetString("acceptor.addr"),
			Transport: v.GetString("acceptor.transport"),
		},
		Cluster: ClusterConfig{
			NatsURL: v.GetString("cluster.natsURL"),
		},
		Timer: TimerConfig{
			Enabled:    v.GetBool("timer.enabled"),
			Tick:       v.GetDuration("timer.tick"),
			WheelSize:  v.GetInt64("timer.wheelSize"),
		},
	}

	// 处理 etcd 配置
	if v.IsSet("cluster.etcd.endpoints") {
		cfg.Cluster.Etcd = &discovery.EtcdConfig{
			Endpoints:            v.GetStringSlice("cluster.etcd.endpoints"),
			Prefix:               v.GetString("cluster.etcd.prefix"),
			HeartbeatTTL:         time.Duration(v.GetInt64("cluster.etcd.heartbeatTTL")) * time.Second,
			SyncServersInterval:  time.Duration(v.GetInt64("cluster.etcd.syncServersInterval")) * time.Second,
		}
	}

	// 处理系统信号
	signalNames := v.GetStringSlice("signals")
	if len(signalNames) > 0 {
		sigs, err := parseSignals(signalNames)
		if err != nil {
			return Config{}, err
		}
		cfg.Signals = sigs
	}

	return cfg, nil
}

// NewConfigFromFile 从 YAML 配置文件加载 Config。
func NewConfigFromFile(path string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	return NewConfigFromViper(v)
}

// fillDefaultValues 注册所有配置项的默认值。
func fillDefaultValues(v *viper.Viper) {
	d := Default()

	v.SetDefault("name", d.Name)
	v.SetDefault("displayName", d.DisplayName)
	v.SetDefault("version", d.Version)
	v.SetDefault("gracefulTimeout", d.GracefulTimeout)
	v.SetDefault("signals", []string{"interrupt", "sigterm"})

	v.SetDefault("heartbeat.interval", d.Heartbeat.Interval)

	v.SetDefault("buffer.writeTimeout", d.Buffer.WriteTimeout)
	v.SetDefault("buffer.messagesBufferSize", d.Buffer.MessagesBufferSize)
	v.SetDefault("buffer.localProcessBufferSize", d.Buffer.LocalProcessBufferSize)

	v.SetDefault("acceptor.addr", d.Acceptor.Addr)
	v.SetDefault("acceptor.transport", d.Acceptor.Transport)

	v.SetDefault("cluster.natsURL", d.Cluster.NatsURL)

	v.SetDefault("timer.enabled", d.Timer.Enabled)
	v.SetDefault("timer.tick", d.Timer.Tick)
	v.SetDefault("timer.wheelSize", d.Timer.WheelSize)

	// etcd 默认值
	v.SetDefault("cluster.etcd.endpoints", []string{"127.0.0.1:2379"})
	v.SetDefault("cluster.etcd.prefix", "projectbid")
	v.SetDefault("cluster.etcd.heartbeatTTL", 30)
	v.SetDefault("cluster.etcd.syncServersInterval", 30)
}

// parseSignals 将信号名称转换为 os.Signal 列表。
func parseSignals(names []string) ([]os.Signal, error) {
	var sigs []os.Signal
	for _, name := range names {
		switch strings.ToLower(name) {
		case "interrupt", "sigint":
			sigs = append(sigs, os.Interrupt)
		case "sigterm":
			sigs = append(sigs, syscall.SIGTERM)
		case "sigquit":
			sigs = append(sigs, syscall.SIGQUIT)
		default:
			return nil, fmt.Errorf("未知信号: %s", name)
		}
	}
	return sigs, nil
}
