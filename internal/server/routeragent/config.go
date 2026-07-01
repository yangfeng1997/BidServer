package routeragent

import (
	"fmt"

	configgen "project/config/gen"
	config "project/internal/core/config"
)

type CommonConfigEntry = config.ConfigEntry[configgen.CommonConfig]
type RouteragentConfigEntry = config.ConfigEntry[configgen.RouteragentConfig]

type ConfigChange struct {
	OldCommon *configgen.CommonConfig
	NewCommon *configgen.CommonConfig
	OldRouteragent *configgen.RouteragentConfig
	NewRouteragent *configgen.RouteragentConfig
}

type ConfigChangeHook func(ConfigChange) error

var (
	commonConfigEntry *CommonConfigEntry
	routeragentConfigEntry *RouteragentConfigEntry
	configChangeHooks []ConfigChangeHook
)

func SetCommonConfigEntry(entry *CommonConfigEntry) {
	commonConfigEntry = entry
}

func SetRouteragentConfigEntry(entry *RouteragentConfigEntry) {
	routeragentConfigEntry = entry
}

func CommonConfig() *configgen.CommonConfig {
	if commonConfigEntry == nil {
		return nil
	}
	return commonConfigEntry.Get()
}

func RouteragentConfig() *configgen.RouteragentConfig {
	if routeragentConfigEntry == nil {
		return nil
	}
	return routeragentConfigEntry.Get()
}

func AddConfigChangeHook(hook ConfigChangeHook) {
	configChangeHooks = append(configChangeHooks, hook)
}

func ReloadConfig() error {
	if commonConfigEntry == nil {
		return fmt.Errorf("common config entry is nil")
	}
	if routeragentConfigEntry == nil {
		return fmt.Errorf("routeragent config entry is nil")
	}

	oldCommon := CommonConfig()
	oldRouteragent := RouteragentConfig()

	if err := commonConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload common config: %w", err)
	}
	if err := routeragentConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload routeragent config: %w", err)
	}

	change := ConfigChange{
		OldCommon: oldCommon,
		NewCommon: CommonConfig(),
		OldRouteragent: oldRouteragent,
		NewRouteragent: RouteragentConfig(),
	}
	for _, hook := range configChangeHooks {
		if err := hook(change); err != nil {
			return err
		}
	}
	return nil
}
