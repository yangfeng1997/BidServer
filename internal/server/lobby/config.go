package lobby

import (
	"fmt"

	configgen "project/config/gen"
	config "project/internal/core/config"
)

type CommonConfigEntry = config.ConfigEntry[configgen.CommonConfig]
type LobbyConfigEntry = config.ConfigEntry[configgen.LobbyConfig]

type ConfigChange struct {
	OldCommon *configgen.CommonConfig
	NewCommon *configgen.CommonConfig
	OldLobby  *configgen.LobbyConfig
	NewLobby  *configgen.LobbyConfig
}

type ConfigChangeHook func(ConfigChange) error

var (
	commonConfigEntry *CommonConfigEntry
	lobbyConfigEntry  *LobbyConfigEntry
	configChangeHooks []ConfigChangeHook
)

func SetCommonConfigEntry(entry *CommonConfigEntry) {
	commonConfigEntry = entry
}

func SetLobbyConfigEntry(entry *LobbyConfigEntry) {
	lobbyConfigEntry = entry
}

func CommonConfig() *configgen.CommonConfig {
	if commonConfigEntry == nil {
		return nil
	}
	return commonConfigEntry.Get()
}

func LobbyConfig() *configgen.LobbyConfig {
	if lobbyConfigEntry == nil {
		return nil
	}
	return lobbyConfigEntry.Get()
}

func AddConfigChangeHook(hook ConfigChangeHook) {
	configChangeHooks = append(configChangeHooks, hook)
}

func ReloadConfig() error {
	if commonConfigEntry == nil {
		return fmt.Errorf("common config entry is nil")
	}
	if lobbyConfigEntry == nil {
		return fmt.Errorf("lobby config entry is nil")
	}

	oldCommon := CommonConfig()
	oldLobby := LobbyConfig()

	if err := commonConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload common config: %w", err)
	}
	if err := lobbyConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload lobby config: %w", err)
	}

	change := ConfigChange{
		OldCommon: oldCommon,
		NewCommon: CommonConfig(),
		OldLobby:  oldLobby,
		NewLobby:  LobbyConfig(),
	}
	for _, hook := range configChangeHooks {
		if err := hook(change); err != nil {
			return err
		}
	}
	return nil
}
