package gate

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
	config "project/internal/core/config"
)

type CommonConfigEntry = config.ConfigEntry[configgen.CommonConfig]
type GateConfigEntry = config.ConfigEntry[configgen.GateConfig]

var commonConfigEntry *CommonConfigEntry
var gateConfigEntry *GateConfigEntry

type ConfigModule struct {
	app.BaseModule
}

func NewConfigModule(commonConfig *CommonConfigEntry, gateConfig *GateConfigEntry) *ConfigModule {
	commonConfigEntry = commonConfig
	gateConfigEntry = gateConfig
	return &ConfigModule{}
}

func CommonConfig() *configgen.CommonConfig {
	if commonConfigEntry == nil {
		return nil
	}
	return commonConfigEntry.Get()
}

func GateConfig() *configgen.GateConfig {
	if gateConfigEntry == nil {
		return nil
	}
	return gateConfigEntry.Get()
}

func (module *ConfigModule) Init(app.App) error {
	if commonConfigEntry == nil {
		return fmt.Errorf("common config entry is nil")
	}
	if gateConfigEntry == nil {
		return fmt.Errorf("gate config entry is nil")
	}
	return nil
}

func (module *ConfigModule) Reload() error {
	if err := commonConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload common config: %w", err)
	}
	if err := gateConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload gate config: %w", err)
	}
	return nil
}
