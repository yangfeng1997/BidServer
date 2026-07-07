package log

import (
	"fmt"
	"io"

	"project/conf/schema/gen"
	"project/internal/core/app"
	"project/pkg/logger"
)

// 三个命名日志实例，AfterInit 之后只读
var (
	Main    logger.Logger
	Res     logger.Logger
	Tracing logger.Logger
)

// logGroupGetter 由服务 Main 在启动时设置，返回当前服务的日志组
var logGroupGetter func() *gen.LogGroupConfig

// SetLogGroupGetter 注册日志组获取函数，服务 Main 在注册 Loader 后调用
func SetLogGroupGetter(fn func() *gen.LogGroupConfig) { logGroupGetter = fn }

// Module 负责从配置构建三个日志实例并管理生命周期
type Module struct {
	app.BaseModule
	closers []io.Closer
}

func NewModule() *Module { return &Module{} }

func (m *Module) Init(_ *app.App) error { return nil }

func (m *Module) AfterInit() error {
	if logGroupGetter == nil {
		return fmt.Errorf("log: SetLogGroupGetter not called")
	}
	lg := logGroupGetter()
	if lg == nil {
		return fmt.Errorf("log: log group not available (config not loaded)")
	}
	for _, e := range []struct {
		cfg    *gen.LogConfig
		target *logger.Logger
	}{
		{lg.Main, &Main},
		{lg.Res, &Res},
		{lg.Tracing, &Tracing},
	} {
		l, closer, err := buildLogger(e.cfg)
		if err != nil {
			return fmt.Errorf("log: %w", err)
		}
		*e.target = l
		m.closers = append(m.closers, closer)
	}
	logger.SetGlobal(Main)
	return nil
}

func (m *Module) Fini() {
	for _, c := range m.closers {
		_ = c.Close()
	}
}

func (m *Module) BeforeStop() {}

// buildLogger 从 typed LogConfig 构造 Logger + Closer
func buildLogger(lc *gen.LogConfig) (logger.Logger, io.Closer, error) {
	if lc == nil {
		return nil, nil, fmt.Errorf("missing log config")
	}
	level, err := parseLevel(lc.Level)
	if err != nil {
		return nil, nil, err
	}
	fc := logger.FileLoggerConfig{
		Level:      level,
		Format:     logger.Format(lc.Format),
		StderrAlso: lc.StderrAlso,
		Rotate: logger.RotateConfig{
			Dir:          lc.Dir,
			Basename:     lc.Basename,
			MaxSizeMB:    int(lc.MaxSizeMb),
			MaxBackups:   int(lc.MaxBackups),
			RotateByHour: lc.RotateByHour,
		},
	}
	return logger.NewZapFileLogger(fc)
}

func parseLevel(s string) (logger.Level, error) {
	var lvl logger.Level
	if err := lvl.UnmarshalText([]byte(s)); err != nil {
		return logger.InfoLevel, err
	}
	return lvl, nil
}
