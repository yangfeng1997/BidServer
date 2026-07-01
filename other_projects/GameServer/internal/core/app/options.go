package app

import (
	"fmt"
	"time"

	"project/pkg/configgen"
)

// Options 由每个服务的启动参数结构体实现
type Options interface {
	Base() *BaseOptions
	Defaults()
}

// BaseOptions 包含所有服务共享的启动参数
type BaseOptions struct {
	ConfigFiles  []string
	ValidateOnly bool

	Tick         time.Duration
	ReadyTimeout time.Duration
	DrainTimeout time.Duration
}

func (o *BaseOptions) Base() *BaseOptions { return o }

func (o *BaseOptions) Defaults() {
	o.ReadyTimeout = 10 * time.Second
}

// CommandMeta 描述一个服务命令
type CommandMeta struct {
	Use   string
	Short string
	Confs []string
}

// ValidateConfig 校验配置文件能否成功加载
func ValidateConfig(configFiles []string) error {
	if len(configFiles) == 0 {
		return fmt.Errorf("config: no config files specified")
	}
	_, err := configgen.LoadFiles[map[string]any](configFiles...)
	if err != nil {
		return fmt.Errorf("config validate: %w", err)
	}
	return nil
}
