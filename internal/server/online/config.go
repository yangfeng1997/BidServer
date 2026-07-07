package online

import (
	"fmt"

	configgen "project/config/gen"
	config "project/internal/core/config"
)

type CommonConfigEntry = config.ConfigEntry[configgen.CommonConfig]
type OnlineConfigEntry = config.ConfigEntry[configgen.OnlineConfig]

type ConfigChange struct {
	OldCommon *configgen.CommonConfig
	NewCommon *configgen.CommonConfig
	OldOnline *configgen.OnlineConfig
	NewOnline *configgen.OnlineConfig
}

type ConfigChangeHook func(ConfigChange) error

var (
	commonConfigEntry *CommonConfigEntry
	onlineConfigEntry *OnlineConfigEntry
	configChangeHooks []ConfigChangeHook
)

func SetCommonConfigEntry(entry *CommonConfigEntry) {
	commonConfigEntry = entry
}

func SetOnlineConfigEntry(entry *OnlineConfigEntry) {
	onlineConfigEntry = entry
}

func CommonConfig() *configgen.CommonConfig {
	if commonConfigEntry == nil {
		return nil
	}
	return commonConfigEntry.Get()
}

func OnlineConfig() *configgen.OnlineConfig {
	if onlineConfigEntry == nil {
		return nil
	}
	return onlineConfigEntry.Get()
}

func AddConfigChangeHook(hook ConfigChangeHook) {
	configChangeHooks = append(configChangeHooks, hook)
}

func ReloadConfig() error {
	if commonConfigEntry == nil {
		return fmt.Errorf("common config entry is nil")
	}
	if onlineConfigEntry == nil {
		return fmt.Errorf("online config entry is nil")
	}

	oldCommon := CommonConfig()
	oldOnline := OnlineConfig()

	if err := commonConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload common config: %w", err)
	}
	if err := onlineConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload online config: %w", err)
	}

	change := ConfigChange{
		OldCommon: oldCommon,
		NewCommon: CommonConfig(),
		OldOnline: oldOnline,
		NewOnline: OnlineConfig(),
	}
	for _, hook := range configChangeHooks {
		if err := hook(change); err != nil {
			return err
		}
	}
	return nil
}
