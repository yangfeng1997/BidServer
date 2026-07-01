package main

const cmdMainTemplate = `package main

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"

	opt "project/internal/core/options"
	"project/internal/core/process"
	"project/internal/server/{{.PackageName}}"
)

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute() error {
	opts := &{{.PackageName}}.Options{}
	bindFlags(opts)
	configureUsage("{{.ServiceName}}")
	pflag.Parse()
	if pflag.NArg() > 0 {
		return fmt.Errorf("unexpected args: %v", pflag.Args())
	}

	if opts.CommonConfigPath == "" {
		return fmt.Errorf("common config path is required")
	}
	if opts.{{.Title}}ConfigPath == "" {
		return fmt.Errorf("{{.PackageName}} config path is required")
	}
	if opts.Daemon {
		started, err := process.StartDaemon()
		if err != nil {
			return fmt.Errorf("start {{.ServiceName}} daemon: %w", err)
		}
		if started {
			return nil
		}
	}

	builder := {{.PackageName}}.New{{.Title}}Builder({{.PackageName}}.Options{
		BaseOptions: opt.BaseOptions{
			PidFile:          opts.PidFile,
			Daemon:           opts.Daemon,
			Pprof:            opts.Pprof,
			PprofAddr:        opts.PprofAddr,
			CommonConfigPath: opts.CommonConfigPath,
		},
		{{.Title}}ConfigPath: opts.{{.Title}}ConfigPath,
	})

	app, err := builder.Build()
	if err != nil {
		return fmt.Errorf("build {{.ServiceName}} app: %w", err)
	}
	if err := process.WritePIDFile(opts.PidFile); err != nil {
		return fmt.Errorf("write {{.ServiceName}} pid file: %w", err)
	}
	defer func() {
		if err := process.RemovePIDFile(opts.PidFile); err != nil {
			fmt.Fprintf(os.Stderr, "remove {{.ServiceName}} pid file: %v\n", err)
		}
	}()

	return app.Startup()
}

func bindFlags(opts *{{.PackageName}}.Options) {
	pflag.StringVarP(&opts.PidFile, "pid-file", "p", "{{.ServiceName}}.pid", "pid file path")
	pflag.StringVar(&opts.CommonConfigPath, "common-config", "", "common config path")
	pflag.StringVar(&opts.{{.Title}}ConfigPath, "{{.PackageName}}-config", "", "{{.PackageName}} config path")
	pflag.BoolVar(&opts.Daemon, "daemon", false, "run as daemon")
	pflag.BoolVar(&opts.Pprof, "pprof", false, "enable pprof server")
	pflag.StringVar(&opts.PprofAddr, "pprof-addr", "127.0.0.1:6060", "pprof listen address")
}

func configureUsage(name string) {
	pflag.CommandLine.SortFlags = false
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "%s starts a {{.ServiceKind}} server.\n\n", name)
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [flags]\n\n", name)
		fmt.Fprintln(os.Stderr, "Flags:")
		pflag.PrintDefaults()
	}
}
`

const serverOptionsTemplate = `package {{.PackageName}}

import opt "project/internal/core/options"

type Options struct {
	opt.BaseOptions
	{{.Title}}ConfigPath string
}
`

const serverConfigTemplate = `package {{.PackageName}}

import (
	"fmt"

	configgen "project/config/gen"
	config "project/internal/core/config"
)

type CommonConfigEntry = config.ConfigEntry[configgen.CommonConfig]
type {{.Title}}ConfigEntry = config.ConfigEntry[configgen.{{.ConfigType}}]

type ConfigChange struct {
	OldCommon *configgen.CommonConfig
	NewCommon *configgen.CommonConfig
	Old{{.Title}} *configgen.{{.ConfigType}}
	New{{.Title}} *configgen.{{.ConfigType}}
}

type ConfigChangeHook func(ConfigChange) error

var (
	commonConfigEntry *CommonConfigEntry
	{{.PackageName}}ConfigEntry *{{.Title}}ConfigEntry
	configChangeHooks []ConfigChangeHook
)

func SetCommonConfigEntry(entry *CommonConfigEntry) {
	commonConfigEntry = entry
}

func Set{{.Title}}ConfigEntry(entry *{{.Title}}ConfigEntry) {
	{{.PackageName}}ConfigEntry = entry
}

func CommonConfig() *configgen.CommonConfig {
	if commonConfigEntry == nil {
		return nil
	}
	return commonConfigEntry.Get()
}

func {{.Title}}Config() *configgen.{{.ConfigType}} {
	if {{.PackageName}}ConfigEntry == nil {
		return nil
	}
	return {{.PackageName}}ConfigEntry.Get()
}

func AddConfigChangeHook(hook ConfigChangeHook) {
	configChangeHooks = append(configChangeHooks, hook)
}

func ReloadConfig() error {
	if commonConfigEntry == nil {
		return fmt.Errorf("common config entry is nil")
	}
	if {{.PackageName}}ConfigEntry == nil {
		return fmt.Errorf("{{.PackageName}} config entry is nil")
	}

	oldCommon := CommonConfig()
	old{{.Title}} := {{.Title}}Config()

	if err := commonConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload common config: %w", err)
	}
	if err := {{.PackageName}}ConfigEntry.Reload(); err != nil {
		return fmt.Errorf("reload {{.PackageName}} config: %w", err)
	}

	change := ConfigChange{
		OldCommon: oldCommon,
		NewCommon: CommonConfig(),
		Old{{.Title}}: old{{.Title}},
		New{{.Title}}: {{.Title}}Config(),
	}
	for _, hook := range configChangeHooks {
		if err := hook(change); err != nil {
			return err
		}
	}
	return nil
}
`

const serverBuilderTemplate = `package {{.PackageName}}

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
	"project/internal/core/logger"
	opt "project/internal/core/options"
)

type Builder struct {
	*app.BaseBuilder
}

func New{{.Title}}Builder(opts Options) *Builder {
	// 1. 必须先加载配置
	commonConfig := mustLoadCommonConfig(opts.CommonConfigPath)
	{{.PackageName}}Config := mustLoad{{.Title}}Config(opts.{{.Title}}ConfigPath)
	SetCommonConfigEntry(commonConfig)
	Set{{.Title}}ConfigEntry({{.PackageName}}Config)

	// 2. 创建LoggerGroup，依赖Option和配置
	loggerGroup := newLoggerGroup(opts.BaseOptions, {{.PackageName}}Config.Get().LoggerGroup)

	baseBuilder := app.NewBaseBuilder(nil)
	baseBuilder.SetDaemon(opts.Daemon)
	baseBuilder.SetPprof(opts.Pprof, opts.PprofAddr)
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

func mustLoad{{.Title}}Config(path string) *{{.Title}}ConfigEntry {
	entry, err := configgen.New{{.Title}}ConfigEntry(path)
	if err != nil {
		panic(fmt.Errorf("load {{.PackageName}} config: %w", err))
	}
	return entry
}

func newLoggerGroup(opts opt.BaseOptions, cfg configgen.LoggerGroupConfig) *logger.LoggerGroup {
	group, err := logger.NewLoggerGroup(opts, cfg)
	if err != nil {
		panic(fmt.Errorf("init {{.PackageName}} logger: %w", err))
	}
	return group
}
`

const schemaTemplate = `syntax = "proto3";

package config;

import "config/schema/options.proto";
import "config/schema/types.proto";

option go_package = "project/config/gen;configgen";

message {{.ConfigType}} {
  option (config.root) = true;
  option (config.server) = "{{.ServiceName}}";
{{if eq .ServiceKind "sidecar"}}
  string sock_path = 1 [(config.required) = true, (config.env) = true];
  string listen_addr = 2 [(config.required) = true, (config.env) = true];
  int32 heartbeat_sec = 3 [(config.required) = true, (config.reload) = true];
  LoggerGroupConfig logger_group = 4;
{{else}}
  string listen_addr = 1 [(config.required) = true, (config.env) = true];
  int32 heartbeat_sec = 2 [(config.required) = true, (config.reload) = true];
  LoggerGroupConfig logger_group = 3;
{{end}}
}
`

const configTemplate = `{{if eq .ServiceKind "sidecar"}}sock_path: "${{.ServiceName}}_sock_path"
listen_addr: "${{.ServiceName}}_listen_addr"
{{else}}listen_addr: "${{.ServiceName}}_listen_addr"
{{end}}heartbeat_sec: 30

logger_group:
  main:
    level: info
    format: console
    stderr_also: true
    dir: "${log_dir}"
    basename: {{.ServiceName}}
    max_size_mb: 100
    max_backups: 72
    rotate_by_hour: true
  res:
    level: info
    format: console
    stderr_also: true
    dir: "${log_dir}"
    basename: {{.ServiceName}}_res
    max_size_mb: 100
    max_backups: 72
    rotate_by_hour: true
  tracing:
    level: info
    format: console
    stderr_also: true
    dir: "${log_dir}"
    basename: {{.ServiceName}}_tracing
    max_size_mb: 100
    max_backups: 72
    rotate_by_hour: true
`

const cmdClaudeTemplate = `# CLAUDE.md

本文件是 ` + "`cmd/{{.ServiceName}}/`" + ` 的局部索引。进入本目录工作时，先读本文件，再看入口 main.go。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- ` + "`{{.ServiceName}}`" + ` 服务入口目录。
- 这里只放 flags 解析、Builder 组装和启动流程，不放业务逻辑。

## 主要文件

- [` + "`main.go`" + `](main.go)

## 快速读法

- 先看 ` + "`main.go`" + ` ` + `，再看对应的 ` + "`internal/server/{{.PackageName}}/`" + `。
- 改启动参数时，要同步 ` + "`internal/server/{{.PackageName}}/`" + ` 的配置和 options。
`

const serverClaudeTemplate = `# CLAUDE.md

本文件是 ` + "`internal/server/{{.PackageName}}/`" + ` 的局部索引。进入本目录工作时，先读本文件，再按需读取相邻源码或上级文档。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- ` + "`{{.PackageName}}`" + ` 服务实现目录。
- 这里放服务 builder、config、options 和业务逻辑。

## 主要文件

- [` + "`builder.go`" + `](builder.go)
- [` + "`config.go`" + `](config.go)
- [` + "`options.go`" + `](options.go)

## 快速读法

- 查启动装配先看 ` + "`builder.go`" + `。
- 查配置入口先看 ` + "`config.go`" + `。
- 查启动参数先看 ` + "`options.go`" + `。
`
