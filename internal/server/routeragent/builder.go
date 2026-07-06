package routeragent

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
	corelogger "project/internal/core/logger"
	opt "project/internal/core/options"
	ragentagent "project/internal/core/ragent/agent"
	"project/internal/core/ragent/agent/discovery/etcd"
	logpkg "project/pkg/logger"
)

type Builder struct {
	*app.BaseBuilder
}

func NewRouteragentBuilder(opts Options) *Builder {
	// 1. 必须先加载配置
	commonConfig := mustLoadCommonConfig(opts.CommonConfigPath)
	routeragentConfig := mustLoadRouteragentConfig(opts.RouteragentConfigPath)
	SetCommonConfigEntry(commonConfig)
	SetRouteragentConfigEntry(routeragentConfig)

	// 2. 创建LoggerGroup，依赖Option和配置
	loggerGroup := newLoggerGroup(opts.BaseOptions, routeragentConfig.Get().LoggerGroup)

	runtime := ragentagent.NewRuntime()
	cfg := routeragentConfig.Get()
	runtime.ApplyConfig(cfg.SockPath, cfg.ListenAddr, cfg.HeartbeatSec)
	if commonConfig.Get() != nil {
		commonCfg := commonConfig.Get()
		prefix := etcd.NodePrefix(commonCfg.Cluster.Name, commonCfg.Cluster.Env, commonCfg.Cluster.WorldId)
		registry, err := etcd.NewEtcdRegistry(commonCfg.Etcd.Endpoints, prefix)
		if err != nil {
			panic(fmt.Errorf("init routeragent etcd registry: %w", err))
		}
		runtime.SetRegistry(registry)
		logpkg.Info("routeragent etcd registry configured", logpkg.String("prefix", prefix))
	}

	baseBuilder := app.NewBaseBuilder(nil)
	baseBuilder.SetDaemon(opts.Daemon)
	baseBuilder.SetPprof(opts.Pprof, opts.PprofAddr)
	baseBuilder.SetNodeID(opts.NodeID)
	baseBuilder.AddModule(NewModule(runtime))
	baseBuilder.AddShutdownHook(loggerGroup.Shutdown)
	baseBuilder.AddReloadHook(ReloadConfig)

	return &Builder{BaseBuilder: baseBuilder}
}

func mustLoadCommonConfig(path string) *CommonConfigEntry {
	entry, err := configgen.NewCommonConfigEntry(path)
	if err != nil {
		panic(fmt.Errorf("load common config: %w", err))
	}
	return entry
}

func mustLoadRouteragentConfig(path string) *RouteragentConfigEntry {
	entry, err := configgen.NewRouteragentConfigEntry(path)
	if err != nil {
		panic(fmt.Errorf("load routeragent config: %w", err))
	}
	return entry
}

func newLoggerGroup(opts opt.BaseOptions, cfg configgen.LoggerGroupConfig) *corelogger.LoggerGroup {
	group, err := corelogger.NewLoggerGroup(opts, cfg)
	if err != nil {
		panic(fmt.Errorf("init routeragent logger: %w", err))
	}
	return group
}
