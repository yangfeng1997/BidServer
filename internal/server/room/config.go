package room

import (
	"fmt"

	configgen "project/config/gen"
	config "project/internal/core/config"
)

type CommonConfigEntry = config.ConfigEntry[configgen.CommonConfig]
type RoomConfigEntry = config.ConfigEntry[configgen.RoomConfig]

type ConfigChange struct {
	OldCommon *configgen.CommonConfig
	NewCommon *configgen.CommonConfig
	OldRoom   *configgen.RoomConfig
	NewRoom   *configgen.RoomConfig
}

type ConfigChangeHook func(ConfigChange) error

var (
	commonConfigEntry *CommonConfigEntry
	roomConfigEntry   *RoomConfigEntry
	configChangeHooks []ConfigChangeHook
)

func SetCommonConfigEntry(entry *CommonConfigEntry) {
	commonConfigEntry = entry
}

func SetRoomConfigEntry(entry *RoomConfigEntry) {
	roomConfigEntry = entry
}

func CommonConfig() *configgen.CommonConfig {
	if commonConfigEntry == nil {
		return nil
	}
	return commonConfigEntry.Get()
}

func RoomConfig() *configgen.RoomConfig {
	if roomConfigEntry == nil {
		return nil
	}
	return roomConfigEntry.Get()
}

func AddConfigChangeHook(hook ConfigChangeHook) {
	configChangeHooks = append(configChangeHooks, hook)
}

func ReloadConfig() error {
	if commonConfigEntry == nil {
		return fmt.Errorf("common config entry is nil")
	}
	if roomConfigEntry == nil {
		return fmt.Errorf("room config entry is nil")
	}

	oldCommon := CommonConfig()
	oldRoom := RoomConfig()

	if err := commonConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload common config: %w", err)
	}
	if err := roomConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload room config: %w", err)
	}

	change := ConfigChange{
		OldCommon: oldCommon,
		NewCommon: CommonConfig(),
		OldRoom:   oldRoom,
		NewRoom:   RoomConfig(),
	}
	for _, hook := range configChangeHooks {
		if err := hook(change); err != nil {
			return err
		}
	}
	return nil
}
