package match

import (
	"fmt"

	configgen "project/config/gen"
	config "project/internal/core/config"
)

type CommonConfigEntry = config.ConfigEntry[configgen.CommonConfig]
type MatchConfigEntry = config.ConfigEntry[configgen.MatchConfig]

type ConfigChange struct {
	OldCommon *configgen.CommonConfig
	NewCommon *configgen.CommonConfig
	OldMatch  *configgen.MatchConfig
	NewMatch  *configgen.MatchConfig
}

type ConfigChangeHook func(ConfigChange) error

var (
	commonConfigEntry *CommonConfigEntry
	matchConfigEntry  *MatchConfigEntry
	configChangeHooks []ConfigChangeHook
)

func SetCommonConfigEntry(entry *CommonConfigEntry) {
	commonConfigEntry = entry
}

func SetMatchConfigEntry(entry *MatchConfigEntry) {
	matchConfigEntry = entry
}

func CommonConfig() *configgen.CommonConfig {
	if commonConfigEntry == nil {
		return nil
	}
	return commonConfigEntry.Get()
}

func MatchConfig() *configgen.MatchConfig {
	if matchConfigEntry == nil {
		return nil
	}
	return matchConfigEntry.Get()
}

func AddConfigChangeHook(hook ConfigChangeHook) {
	configChangeHooks = append(configChangeHooks, hook)
}

func ReloadConfig() error {
	if commonConfigEntry == nil {
		return fmt.Errorf("common config entry is nil")
	}
	if matchConfigEntry == nil {
		return fmt.Errorf("match config entry is nil")
	}

	oldCommon := CommonConfig()
	oldMatch := MatchConfig()

	if err := commonConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload common config: %w", err)
	}
	if err := matchConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload match config: %w", err)
	}

	change := ConfigChange{
		OldCommon: oldCommon,
		NewCommon: CommonConfig(),
		OldMatch:  oldMatch,
		NewMatch:  MatchConfig(),
	}
	for _, hook := range configChangeHooks {
		if err := hook(change); err != nil {
			return err
		}
	}
	return nil
}
