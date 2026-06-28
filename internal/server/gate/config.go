package gate

import (
	"fmt"

	configgen "project/config/gen"
	config "project/internal/core/config"
)

type CommonConfigEntry = config.ConfigEntry[configgen.CommonConfig]
type GateConfigEntry = config.ConfigEntry[configgen.GateConfig]

type ConfigChange struct {
	OldCommon *configgen.CommonConfig
	NewCommon *configgen.CommonConfig
	OldGate   *configgen.GateConfig
	NewGate   *configgen.GateConfig
}

type ConfigChangeHook func(ConfigChange) error

var (
	commonConfigEntry *CommonConfigEntry
	gateConfigEntry   *GateConfigEntry
	configChangeHooks []ConfigChangeHook
)

func SetCommonConfigEntry(entry *CommonConfigEntry) {
	commonConfigEntry = entry
}

func SetGateConfigEntry(entry *GateConfigEntry) {
	gateConfigEntry = entry
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

func AddConfigChangeHook(hook ConfigChangeHook) {
	configChangeHooks = append(configChangeHooks, hook)
}

func ReloadConfig() error {
	if commonConfigEntry == nil {
		return fmt.Errorf("common config entry is nil")
	}
	if gateConfigEntry == nil {
		return fmt.Errorf("gate config entry is nil")
	}

	oldCommon := CommonConfig()
	oldGate := GateConfig()

	if err := commonConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload common config: %w", err)
	}
	if err := gateConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload gate config: %w", err)
	}

	change := ConfigChange{
		OldCommon: oldCommon,
		NewCommon: CommonConfig(),
		OldGate:   oldGate,
		NewGate:   GateConfig(),
	}
	for _, hook := range configChangeHooks {
		if err := hook(change); err != nil {
			return err
		}
	}
	return nil
}
