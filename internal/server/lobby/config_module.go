package lobby

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
	config "project/internal/core/config"
)

var commonConfigEntry *config.ConfigEntry[configgen.CommonConfig]
var lobbyConfigEntry *config.ConfigEntry[configgen.LobbyConfig]

type ConfigModule struct {
	app.BaseModule
}

func NewConfigModule(commonEntry *config.ConfigEntry[configgen.CommonConfig], lobbyEntry *config.ConfigEntry[configgen.LobbyConfig]) *ConfigModule {
	commonConfigEntry = commonEntry
	lobbyConfigEntry = lobbyEntry
	return &ConfigModule{}
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

func (module *ConfigModule) Init(app.App) error {
	if commonConfigEntry == nil {
		return fmt.Errorf("common config entry is nil")
	}
	if lobbyConfigEntry == nil {
		return fmt.Errorf("lobby config entry is nil")
	}
	return nil
}

func (module *ConfigModule) Reload() error {
	if err := commonConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload common config: %w", err)
	}
	if err := lobbyConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload lobby config: %w", err)
	}
	return nil
}
